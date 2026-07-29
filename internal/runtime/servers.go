package runtime

import (
	"encoding/json"
	"fmt"

	wailsconfigstore "github.com/AndreiTelteu/wails-configstore"
)

// TunnelConfig describes an optional local SSH port-forward for a database
// server. It is persisted alongside the connection definition.
type TunnelConfig struct {
	Enabled    bool   `json:"enabled"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	AuthMethod string `json:"authMethod"`
	PrivateKey string `json:"privateKey"`
	Passphrase string `json:"passphrase"`
}

type ServerConfig struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Host     string       `json:"host"`
	Port     int          `json:"port"`
	Username string       `json:"username"`
	Password string       `json:"password"`
	Tunnel   TunnelConfig `json:"tunnel"`
}

type ServersConfig struct {
	List []ServerConfig `json:"list"`
}

func (s *ServersConfig) FindByID(id string) *ServerConfig {
	for index := range s.List {
		if s.List[index].ID == id {
			return &s.List[index]
		}
	}
	return nil
}

// ConfigStore is the minimal config-store contract the runtime package
// needs; *wailsconfigstore.ConfigStore satisfies it.
type ConfigStore interface {
	Get(filename string, defaultValue string) (wailsconfigstore.Config, error)
}

func GetServersConfig(conf ConfigStore) (*ServersConfig, error) {
	data, err := conf.Get("servers.json", `{"list":[]}`)
	if err != nil {
		return nil, fmt.Errorf("read servers configuration: %w", err)
	}

	var serversConfig ServersConfig
	if err := json.Unmarshal([]byte(data), &serversConfig); err != nil {
		return nil, fmt.Errorf("parse servers configuration: %w", err)
	}
	return &serversConfig, nil
}

// NewServerConfigLoader returns the Session config lookup bound to a config
// store. It is the exported way for hosts (main package) to wire the
// persisted connection catalogue into a Session.
func NewServerConfigLoader(store ConfigStore) func(serverID string) (*ServerConfig, error) {
	return func(serverID string) (*ServerConfig, error) {
		cfg, err := GetServersConfig(store)
		if err != nil {
			return nil, err
		}
		return cfg.FindByID(serverID), nil
	}
}
