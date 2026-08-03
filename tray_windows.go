package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

const (
	trayCallbackMessage = 0x8001
	trayMenuShow        = 1
	trayMenuExit        = 2
)

var (
	trayUser32              = windows.NewLazySystemDLL("user32.dll")
	trayShell32             = windows.NewLazySystemDLL("shell32.dll")
	trayKernel32            = windows.NewLazySystemDLL("kernel32.dll")
	trayRegisterClassEx     = trayUser32.NewProc("RegisterClassExW")
	trayUnregisterClass     = trayUser32.NewProc("UnregisterClassW")
	trayCreateWindowEx      = trayUser32.NewProc("CreateWindowExW")
	trayDestroyWindow       = trayUser32.NewProc("DestroyWindow")
	trayDefWindowProc       = trayUser32.NewProc("DefWindowProcW")
	trayGetMessage          = trayUser32.NewProc("GetMessageW")
	trayTranslateMessage    = trayUser32.NewProc("TranslateMessage")
	trayDispatchMessage     = trayUser32.NewProc("DispatchMessageW")
	trayPostMessage         = trayUser32.NewProc("PostMessageW")
	trayPostQuitMessage     = trayUser32.NewProc("PostQuitMessage")
	trayRegisterWindowMsg   = trayUser32.NewProc("RegisterWindowMessageW")
	trayCreatePopupMenu     = trayUser32.NewProc("CreatePopupMenu")
	trayAppendMenu          = trayUser32.NewProc("AppendMenuW")
	trayDestroyMenu         = trayUser32.NewProc("DestroyMenu")
	trayTrackPopupMenu      = trayUser32.NewProc("TrackPopupMenu")
	trayGetCursorPos        = trayUser32.NewProc("GetCursorPos")
	traySetForegroundWindow = trayUser32.NewProc("SetForegroundWindow")
	trayLoadImage           = trayUser32.NewProc("LoadImageW")
	trayDestroyIcon         = trayUser32.NewProc("DestroyIcon")
	trayShellNotifyIcon     = trayShell32.NewProc("Shell_NotifyIconW")
	trayGetModuleHandle     = trayKernel32.NewProc("GetModuleHandleW")
	trayWindowProcCallback  = windows.NewCallback(trayWindowProc)
	activeTray              atomic.Pointer[windowsTray]
)

type trayPoint struct {
	X int32
	Y int32
}

type trayMessage struct {
	Window  windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   trayPoint
	Private uint32
}

type trayWindowClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  windows.Handle
}

type trayNotifyIconData struct {
	Size             uint32
	Window           windows.Handle
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             windows.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	GUIDItem         windows.GUID
	BalloonIcon      windows.Handle
}

type windowsTray struct {
	app          *App
	onShow       func()
	onExit       func()
	onRightClick func()

	window          atomic.Uintptr
	stopRequested   atomic.Bool
	stopOnce        sync.Once
	instance        windows.Handle
	menu            windows.Handle
	icon            windows.Handle
	notify          trayNotifyIconData
	className       *uint16
	taskbarCreated  uint32
	iconPath        string
	classRegistered bool
	iconAdded       bool
}

func (a *App) startTray() {
	if !a.trayStarted.CompareAndSwap(false, true) {
		return
	}
	tray := newWindowsTray(a)
	a.tray.Store(tray)
	go tray.run()
}

func newWindowsTray(app *App) *windowsTray {
	tray := &windowsTray{
		app:    app,
		onShow: app.ShowWindow,
		onExit: app.ExitApplication,
	}
	tray.onRightClick = tray.showMenu
	return tray
}

func (tray *windowsTray) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer func() {
		tray.cleanup()
		activeTray.CompareAndSwap(tray, nil)
		tray.app.trayStarted.Store(false)
	}()

	activeTray.Store(tray)
	if err := tray.initialize(); err != nil {
		tray.app.setTrayError(err)
		return
	}
	if tray.stopRequested.Load() {
		tray.Stop()
	}

	var message trayMessage
	for {
		result, _, callErr := trayGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		switch int32(result) {
		case -1:
			tray.app.setTrayError(fmt.Errorf("读取托盘消息失败: %w", callErr))
			return
		case 0:
			return
		default:
			trayTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
			trayDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
		}
	}
}

func (tray *windowsTray) initialize() error {
	const (
		csDoubleClicks = 0x0008
		mfString       = 0x0000
		mfSeparator    = 0x0800
		nifMessage     = 0x0001
		nifIcon        = 0x0002
		nifTip         = 0x0004
		nimAdd         = 0x0000
	)

	module, _, callErr := trayGetModuleHandle.Call(0)
	if module == 0 {
		return fmt.Errorf("获取托盘模块句柄失败: %w", callErr)
	}
	tray.instance = windows.Handle(module)

	className, err := windows.UTF16PtrFromString("LycheeTitanCleannerTrayWindow")
	if err != nil {
		return err
	}
	tray.className = className
	windowClass := trayWindowClass{
		Style:     csDoubleClicks,
		WndProc:   trayWindowProcCallback,
		Instance:  tray.instance,
		ClassName: className,
	}
	windowClass.Size = uint32(unsafe.Sizeof(windowClass))
	registered, _, callErr := trayRegisterClassEx.Call(uintptr(unsafe.Pointer(&windowClass)))
	if registered == 0 {
		return fmt.Errorf("注册托盘窗口类失败: %w", callErr)
	}
	tray.classRegistered = true

	windowName, _ := windows.UTF16PtrFromString("")
	window, _, callErr := trayCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0,
		0, 0, 0, 0,
		0, 0,
		uintptr(tray.instance),
		0,
	)
	if window == 0 {
		return fmt.Errorf("创建托盘消息窗口失败: %w", callErr)
	}
	tray.window.Store(window)

	taskbarMessage, _ := windows.UTF16PtrFromString("TaskbarCreated")
	value, _, callErr := trayRegisterWindowMsg.Call(uintptr(unsafe.Pointer(taskbarMessage)))
	if value == 0 {
		return fmt.Errorf("注册任务栏重建消息失败: %w", callErr)
	}
	tray.taskbarCreated = uint32(value)

	menu, _, callErr := trayCreatePopupMenu.Call()
	if menu == 0 {
		return fmt.Errorf("创建托盘菜单失败: %w", callErr)
	}
	tray.menu = windows.Handle(menu)
	showText, _ := windows.UTF16PtrFromString("显示主窗口")
	if ok, _, appendErr := trayAppendMenu.Call(menu, mfString, trayMenuShow, uintptr(unsafe.Pointer(showText))); ok == 0 {
		return fmt.Errorf("添加显示菜单失败: %w", appendErr)
	}
	if ok, _, appendErr := trayAppendMenu.Call(menu, mfSeparator, 0, 0); ok == 0 {
		return fmt.Errorf("添加托盘分隔线失败: %w", appendErr)
	}
	exitText, _ := windows.UTF16PtrFromString("退出程序")
	if ok, _, appendErr := trayAppendMenu.Call(menu, mfString, trayMenuExit, uintptr(unsafe.Pointer(exitText))); ok == 0 {
		return fmt.Errorf("添加退出菜单失败: %w", appendErr)
	}

	icon, iconPath, err := loadTrayIcon(trayIcon)
	if err != nil {
		return err
	}
	tray.icon = icon
	tray.iconPath = iconPath

	tray.notify = trayNotifyIconData{
		Window:          windows.Handle(window),
		ID:              1,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: trayCallbackMessage,
		Icon:            icon,
	}
	tray.notify.Size = uint32(unsafe.Sizeof(tray.notify))
	tip, _ := windows.UTF16FromString("荔枝时光服进程专清工具")
	copy(tray.notify.Tip[:], tip)
	if ok, _, notifyErr := trayShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&tray.notify))); ok == 0 {
		return fmt.Errorf("添加系统托盘图标失败: %w", notifyErr)
	}
	tray.iconAdded = true
	return nil
}

func loadTrayIcon(data []byte) (windows.Handle, string, error) {
	const (
		imageIcon      = 1
		lrLoadFromFile = 0x0010
		lrDefaultSize  = 0x0040
	)
	hash := sha256.Sum256(data)
	path := filepath.Join(os.TempDir(), fmt.Sprintf("lychee-titan-cleanner-tray-%x.ico", hash[:8]))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return 0, "", fmt.Errorf("写入托盘图标失败: %w", err)
	}
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, path, err
	}
	icon, _, callErr := trayLoadImage.Call(0, uintptr(unsafe.Pointer(pathPointer)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize)
	if icon == 0 {
		return 0, path, fmt.Errorf("加载托盘图标失败: %w", callErr)
	}
	return windows.Handle(icon), path, nil
}

func trayWindowProc(window windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	tray := activeTray.Load()
	if tray == nil {
		result, _, _ := trayDefWindowProc.Call(uintptr(window), uintptr(message), wParam, lParam)
		return result
	}
	return tray.windowProc(window, message, wParam, lParam)
}

func (tray *windowsTray) windowProc(window windows.Handle, message uint32, wParam, lParam uintptr) uintptr {
	const (
		wmClose            = 0x0010
		wmDestroy          = 0x0002
		wmLeftButtonDouble = 0x0203
		wmRightButtonUp    = 0x0205
	)
	switch message {
	case trayCallbackMessage:
		tray.handleMouseEvent(uint32(lParam))
		return 0
	case wmClose:
		trayDestroyWindow.Call(uintptr(window))
		return 0
	case wmDestroy:
		tray.window.Store(0)
		tray.removeIcon()
		trayPostQuitMessage.Call(0)
		return 0
	case tray.taskbarCreated:
		tray.readdIcon()
		return 0
	default:
		result, _, _ := trayDefWindowProc.Call(uintptr(window), uintptr(message), wParam, lParam)
		return result
	}
}

func (tray *windowsTray) handleMouseEvent(event uint32) {
	const (
		wmLeftButtonDouble = 0x0203
		wmRightButtonUp    = 0x0205
	)
	switch event {
	case wmLeftButtonDouble:
		tray.onShow()
	case wmRightButtonUp:
		tray.onRightClick()
	}
}

func (tray *windowsTray) showMenu() {
	const (
		tpmRightButton = 0x0002
		tpmBottomAlign = 0x0020
		tpmNoNotify    = 0x0080
		tpmReturnCmd   = 0x0100
		wmNull         = 0x0000
	)
	var cursor trayPoint
	if ok, _, _ := trayGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor))); ok == 0 {
		return
	}
	window := tray.window.Load()
	traySetForegroundWindow.Call(window)
	command, _, _ := trayTrackPopupMenu.Call(
		uintptr(tray.menu),
		tpmRightButton|tpmBottomAlign|tpmNoNotify|tpmReturnCmd,
		uintptr(cursor.X),
		uintptr(cursor.Y),
		0,
		window,
		0,
	)
	trayPostMessage.Call(window, wmNull, 0, 0)
	tray.handleMenuCommand(command)
}

func (tray *windowsTray) handleMenuCommand(command uintptr) {
	switch command {
	case trayMenuShow:
		tray.onShow()
	case trayMenuExit:
		tray.onExit()
	}
}

func (tray *windowsTray) readdIcon() {
	const nimAdd = 0x0000
	if tray.window.Load() == 0 {
		return
	}
	if ok, _, _ := trayShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&tray.notify))); ok != 0 {
		tray.iconAdded = true
	}
}

func (tray *windowsTray) removeIcon() {
	const nimDelete = 0x0002
	if tray.iconAdded {
		trayShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&tray.notify)))
		tray.iconAdded = false
	}
}

func (tray *windowsTray) cleanup() {
	tray.removeIcon()
	if window := tray.window.Swap(0); window != 0 {
		trayDestroyWindow.Call(window)
	}
	if tray.menu != 0 {
		trayDestroyMenu.Call(uintptr(tray.menu))
		tray.menu = 0
	}
	if tray.icon != 0 {
		trayDestroyIcon.Call(uintptr(tray.icon))
		tray.icon = 0
	}
	if tray.classRegistered && tray.className != nil {
		trayUnregisterClass.Call(uintptr(unsafe.Pointer(tray.className)), uintptr(tray.instance))
		tray.classRegistered = false
	}
	if tray.iconPath != "" {
		_ = os.Remove(tray.iconPath)
	}
}

func (tray *windowsTray) Stop() {
	tray.stopRequested.Store(true)
	window := tray.window.Load()
	if window == 0 {
		return
	}
	tray.stopOnce.Do(func() {
		const wmClose = 0x0010
		trayPostMessage.Call(window, wmClose, 0, 0)
	})
}

func (a *App) setTrayError(err error) {
	a.mu.Lock()
	a.lastError = "系统托盘初始化失败: " + err.Error()
	a.mu.Unlock()
}

func (a *App) HideToTray() {
	if a.ctx != nil {
		wailsRuntime.WindowHide(a.ctx)
	}
}

func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	wailsRuntime.WindowShow(a.ctx)
	wailsRuntime.WindowUnminimise(a.ctx)
	wailsRuntime.WindowSetAlwaysOnTop(a.ctx, true)
	wailsRuntime.WindowSetAlwaysOnTop(a.ctx, false)
}

func (a *App) ExitApplication() {
	if !a.quitting.CompareAndSwap(false, true) {
		return
	}
	if tray := a.tray.Load(); tray != nil {
		tray.Stop()
	}
	if a.ctx != nil {
		wailsRuntime.Quit(a.ctx)
	}
}

func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting.Load() {
		return false
	}
	wailsRuntime.WindowHide(ctx)
	return true
}
