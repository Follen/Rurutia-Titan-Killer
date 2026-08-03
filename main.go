package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	windowOptions "github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if serviceProcess, err := runAsCleanupService(); serviceProcess {
		if err != nil {
			recordServiceDiagnostic("service.run", err)
		}
		return
	} else if err != nil {
		recordServiceDiagnostic("service.detect", err)
	}

	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "荔枝时光服进程专清工具",
		Width:     940,
		Height:    720,
		MinWidth:  780,
		MinHeight: 620,
		Frameless: true,
		Windows: &windowOptions.Options{
			DisableFramelessWindowDecorations: true,
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 245, G: 246, B: 247, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.beforeClose,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "c9d695ce-219c-4f1b-a0e4-9e38a2c0993e",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				app.ShowWindow()
			},
		},
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
