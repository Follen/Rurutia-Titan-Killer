package main

import (
	"os"
	"runtime"
	"testing"
)

func TestTrayInteractionRouting(t *testing.T) {
	tray := &windowsTray{}
	showCount := 0
	exitCount := 0
	rightClickCount := 0
	tray.onShow = func() { showCount++ }
	tray.onExit = func() { exitCount++ }
	tray.onRightClick = func() { rightClickCount++ }

	tray.handleMouseEvent(0x0203)
	tray.handleMouseEvent(0x0205)
	tray.handleMenuCommand(trayMenuShow)
	tray.handleMenuCommand(trayMenuExit)

	if showCount != 2 {
		t.Fatalf("show callback count = %d, want 2", showCount)
	}
	if rightClickCount != 1 {
		t.Fatalf("right-click callback count = %d, want 1", rightClickCount)
	}
	if exitCount != 1 {
		t.Fatalf("exit callback count = %d, want 1", exitCount)
	}
}

func TestWindowsTrayIntegration(t *testing.T) {
	if os.Getenv("FENGLAN_TRAY_INTEGRATION") != "1" {
		t.Skip("set FENGLAN_TRAY_INTEGRATION=1 to create a real notification icon")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	app := NewApp()
	tray := newWindowsTray(app)
	showCount := 0
	rightClickCount := 0
	tray.onShow = func() { showCount++ }
	tray.onRightClick = func() { rightClickCount++ }
	activeTray.Store(tray)
	defer func() {
		tray.cleanup()
		activeTray.CompareAndSwap(tray, nil)
	}()

	if err := tray.initialize(); err != nil {
		t.Fatalf("initialize tray: %v", err)
	}
	if tray.window.Load() == 0 || tray.menu == 0 || tray.icon == 0 || !tray.iconAdded {
		t.Fatalf("tray resources incomplete: window=%v menu=%v icon=%v added=%t", tray.window.Load(), tray.menu, tray.icon, tray.iconAdded)
	}

	tray.windowProc(tray.notify.Window, trayCallbackMessage, 0, 0x0203)
	tray.windowProc(tray.notify.Window, trayCallbackMessage, 0, 0x0205)
	if showCount != 1 || rightClickCount != 1 {
		t.Fatalf("native event routing: show=%d rightClick=%d", showCount, rightClickCount)
	}
}
