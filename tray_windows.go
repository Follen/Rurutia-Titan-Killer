package main

import (
	"context"
	_ "embed"

	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed build/windows/icon.ico
var trayIcon []byte

func (a *App) startTray() {
	if !a.trayStarted.CompareAndSwap(false, true) {
		return
	}
	go systray.Run(a.trayReady, func() {})
}

func (a *App) trayReady() {
	systray.SetIcon(trayIcon)
	systray.SetTitle("露露时光服残留专杀")
	systray.SetTooltip("露露时光服残留专杀")

	showItem := systray.AddMenuItem("显示主窗口", "显示守护面板")
	systray.AddSeparator()
	exitItem := systray.AddMenuItem("退出程序", "停止守护并退出")

	go func() {
		for {
			select {
			case <-showItem.ClickedCh:
				a.ShowWindow()
			case <-exitItem.ClickedCh:
				a.ExitApplication()
				return
			}
		}
	}()
}

func (a *App) HideToTray() {
	if a.ctx != nil {
		runtime.WindowHide(a.ctx)
	}
}

func (a *App) ShowWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

func (a *App) ExitApplication() {
	if !a.quitting.CompareAndSwap(false, true) {
		return
	}
	systray.Quit()
	if a.ctx != nil {
		runtime.Quit(a.ctx)
	}
}

func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting.Load() {
		return false
	}
	runtime.WindowHide(ctx)
	return true
}
