package main

import (
	"embed"
	"fmt"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func printError(err error) {
	if err != nil {
		fmt.Println(err)
	}
}

func main() {
	initLog()
	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "檔案管理",
		Width:            1440,
		Height:           860,
		MinWidth:         980,
		MinHeight:        600,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 238, G: 240, B: 244, A: 255},
		OnStartup:        app.startup,
		Bind:             []interface{}{app},
	})
	if err != nil {
		printError(err)
		logErr("啟動", err)
		showFatal("無法啟動檔案管理：\n" + err.Error() + "\n\n若訊息與 WebView2 有關，請安裝 Microsoft Edge WebView2 Runtime。")
	}
}
