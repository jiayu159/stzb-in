package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"
	"stzbHelper/global"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	log.SetOutput(global.LogW)
	go runNpcap()
	// Create an instance of the app structure
	app := NewApp()

	userDir, _ := os.UserConfigDir()

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "stzbHelper",
		Width:     1600,
		Height:    900,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 255, G: 255, B: 255, A: 1},
		Windows: &windows.Options{
			// 固定 WebView2 数据目录(不随 exe 文件名变化)，避免加壳/改名后 EBWebView 目录含非法字符
			WebviewUserDataPath: filepath.Join(userDir, "stzbHelper"),
		},
		OnStartup:        app.startup,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
