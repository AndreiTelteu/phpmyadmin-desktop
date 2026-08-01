package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/wailsapp/wails/v3/pkg/application"

	wailsconfigstore "github.com/AndreiTelteu/wails-configstore"

	"github.com/andreitelteu/phpmyadmin-desktop/internal/runtime"
)

// App is the bound application service. Keep all calls that touch local
// configuration or the filesystem in this service; the Solid frontend must not
// access the host directly.
type App struct {
	serverID    string
	configStore *wailsconfigstore.ConfigStore
	session     *runtime.Session
}

func NewApp(serverID string) *App {
	configStore, err := wailsconfigstore.NewConfigStore("phpMyAdmin Desktop")
	if err != nil {
		panic(fmt.Errorf("initialize configuration store: %w", err))
	}

	app := &App{
		serverID:    serverID,
		configStore: configStore,
	}
	if serverID != "" {
		sess := runtime.NewSession(runtime.NewDefaultManager())
		sess.SetConfigLoader(runtime.NewServerConfigLoader(configStore))
		app.session = sess
	}
	return app
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

// SessionStart installs/reuses the runtime, optionally opens the SSH tunnel,
// starts the FrankenPHP child process and returns the loopback URL the
// frontend navigates to. Only available in a dedicated -serverId process.
func (a *App) SessionStart() (string, error) {
	if a.session == nil {
		return "", fmt.Errorf("this window is not attached to a dedicated connection session")
	}
	return a.session.Start(context.Background(), a.serverID)
}

// SessionStatus exposes the session phase for progress rendering.
func (a *App) SessionStatus() (string, error) {
	if a.session == nil {
		return `{"phase":"idle"}`, nil
	}
	snap := a.session.Snapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SessionReconnectTunnel reconnects an active SSH tunnel on its existing local
// loopback port. It deliberately does not restart the phpMyAdmin runtime.
func (a *App) SessionReconnectTunnel() error {
	if a.session == nil {
		return fmt.Errorf("this window is not attached to a dedicated connection session")
	}
	return a.session.ReconnectTunnel(context.Background())
}

// SessionStop tears down the session runtime and tunnel. It is idempotent.
func (a *App) SessionStop() {
	if a.session != nil {
		a.session.Stop()
	}
}

// FindServerName resolves a connection name for window composition.
func (a *App) FindServerName(serverID string) (string, error) {
	servers, err := runtime.GetServersConfig(a.configStore)
	if err != nil {
		return "", err
	}
	if server := servers.FindByID(serverID); server != nil {
		return server.Name, nil
	}
	return "", nil
}

func (a *App) shutdown() {
	if a.session != nil {
		a.session.Stop()
	}
}
