package main

import (
	"embed"
	"flag"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/logger"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

// TODO: Set default starting method.
var serverId = flag.String("serverId", "nil", "Server ID to open PMA for")

func main() {
	flag.Parse()
	app := NewApp(*serverId)

	opts := &options.App{
		Title:  "phpMyAdmin Desktop",
		Width:  400,
		Height: 400,
		// MinWidth:          1024,
		// MinHeight:         768,
		// MaxWidth:          1280,
		// MaxHeight:         800,
		DisableResize:     false,
		Fullscreen:        false,
		Frameless:         false,
		StartHidden:       false,
		HideWindowOnClose: false,
		BackgroundColour:  &options.RGBA{R: 255, G: 255, B: 255, A: 255},
		Assets:            assets,
		Menu:              nil,
		Logger:            nil,
		LogLevel:          logger.DEBUG,
		OnStartup:         app.startup,
		OnDomReady:        app.domReady,
		OnBeforeClose:     app.beforeClose,
		OnShutdown:        app.shutdown,
		WindowStartState:  options.Normal,
		Bind: []interface{}{
			app,
		},
		// Windows platform specific options
		Windows: &windows.Options{
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			DisableWindowIcon:    false,
			// DisableFramelessWindowDecorations: false,
			WebviewUserDataPath: "",
			BackdropType:        windows.Mica,
		},
		// Mac platform specific options
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				HideTitle:                  false,
				HideTitleBar:               false,
				FullSizeContent:            false,
				UseToolbar:                 true,
				HideToolbarSeparator:       true,
			},
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
			About: &mac.AboutInfo{
				Title:   "phpMyAdmin Desktop",
				Message: "",
				Icon:    icon,
			},
		},
	}
	if serverId != nil && *serverId != "nil" {
		opts.Title = "phpMyAdmin Desktop - " + *serverId
		opts.Width = 1024
		opts.Height = 768
		opts.Windows.WebviewIsTransparent = false
		opts.Windows.WindowIsTranslucent = false
		opts.Windows.BackdropType = windows.None

	}

	err := wails.Run(opts)
	if err != nil {
		log.Fatal(err)
	}
}
