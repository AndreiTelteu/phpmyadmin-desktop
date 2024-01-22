package main

import (
	"encoding/json"
	"fmt"

	wailsconfigstore "github.com/AndreiTelteu/wails-configstore"
)

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
	Id       string       `json:"id"`
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

func (s *ServersConfig) FindById(id string) *ServerConfig {
	for _, server := range s.List {
		if server.Id == id {
			return &server
		}
	}
	return nil
}

func GetServersConfig(conf *wailsconfigstore.ConfigStore) *ServersConfig {
	data, err := conf.Get("servers.json", `{"list":[]}`)
	if err != nil {
		fmt.Println("could not read the servers config file:", err)
		return nil
	}
	var serversConfig ServersConfig
	err = json.Unmarshal([]byte(data), &serversConfig)
	if err != nil {
		fmt.Println("could not parse servers config data:", err)
		return nil
	}
	return &serversConfig
}
