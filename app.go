package main

import (
	"context"
	"os"
	"os/exec"
)

// App struct
type App struct {
	ctx      context.Context
	serverId string
}

// NewApp creates a new App application struct
func NewApp(serverId string) *App {
	return &App{
		serverId: serverId,
	}
}

// startup is called at application startup
func (a *App) startup(ctx context.Context) {
	// Perform your setup here
	a.ctx = ctx
}

// domReady is called after front-end resources have been loaded
func (a App) domReady(ctx context.Context) {
	// Add your action here
}

// beforeClose is called when the application is about to quit,
// either by clicking the window close button or calling runtime.Quit.
// Returning true will cause the application to continue, false will continue shutdown as normal.
func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	return false
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	// Perform your teardown here
}

// Greet returns a greeting for the given name
func (a *App) NewWindow(serverId string) error {
	path, _ := os.Executable()

	var cmd string
	var args []string
	cmd = path
	args = []string{"-serverId", serverId}
	return exec.Command(cmd, args...).Start()
}
