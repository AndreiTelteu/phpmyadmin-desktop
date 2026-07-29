package main

import (
	"embed"
	"flag"
	"log"
	"log/slog"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var icon []byte

var serverID = flag.String("serverId", "", "Server ID to open in a dedicated phpMyAdmin window")

func main() {
	flag.Parse()

	appService := NewApp(*serverID)
	app := application.New(application.Options{
		Name:        "phpMyAdmin Desktop",
		Description: "A local phpMyAdmin launcher with saved remote database connections and SSH tunnels.",
		Icon:        icon,
		Logger:      application.DefaultLogger(slog.LevelDebug),
		Services: []application.Service{
			application.NewService(appService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	app.OnShutdown(appService.shutdown)

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "phpMyAdmin Desktop",
		Width:            540,
		Height:           600,
		BackgroundColour: application.NewRGBA(255, 255, 255, 255),
		URL:              "/",
		Windows: application.WindowsWindow{
			BackdropType: application.Mica,
		},
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
