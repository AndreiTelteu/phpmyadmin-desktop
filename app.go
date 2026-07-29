package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/wailsapp/wails/v3/pkg/application"

	wailsconfigstore "github.com/AndreiTelteu/wails-configstore"
)

// App is the bound application service. Keep all calls that touch local
// configuration or the filesystem in this service; the Solid frontend must not
// access the host directly.
type App struct {
	serverID    string
	configStore *wailsconfigstore.ConfigStore
}

func NewApp(serverID string) *App {
	configStore, err := wailsconfigstore.NewConfigStore("phpMyAdmin Desktop")
	if err != nil {
		panic(fmt.Errorf("initialize configuration store: %w", err))
	}

	return &App{
		serverID:    serverID,
		configStore: configStore,
	}
}

// GetServerID identifies the selected connection when this process was launched
// specifically for that connection.
func (a *App) GetServerID() string {
	return a.serverID
}

// GetServersJSON returns the persisted connection catalogue. Credentials are
// stored locally by the existing config-store implementation.
func (a *App) GetServersJSON() (string, error) {
	config, err := a.configStore.Get("servers.json", `{"list":[]}`)
	if err != nil {
		return "", err
	}
	return string(config), nil
}

// SaveServersJSON persists the connection catalogue exactly as supplied by the
// frontend. Validation belongs in the connection form when it is implemented.
func (a *App) SaveServersJSON(value string) error {
	return a.configStore.Set("servers.json", wailsconfigstore.Config(value))
}

// NewWindow starts a second app process for a selected connection, preserving
// the original process-per-connection design.
func (a *App) NewWindow(serverID string) error {
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	return exec.Command(path, "-serverId", serverID).Start()
}

// ChoosePrivateKey opens the platform file picker at ~/.ssh when available.
func (a *App) ChoosePrivateKey() (string, error) {
	directory := filepath.Join(xdg.Home, ".ssh")
	return application.Get().Dialog.OpenFile().
		SetTitle("Choose SSH private key").
		SetDirectory(directory).
		CanChooseFiles(true).
		PromptForSingleSelection()
}

func (a *App) shutdown() {}
