package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server     ServerConfig     `toml:"server"`
	SSH        SSHConfig        `toml:"ssh"`
	Web        WebConfig        `toml:"web"`
	Database   DatabaseConfig   `toml:"database"`
	Federation FederationConfig `toml:"federation"`
	Limits     LimitsConfig     `toml:"limits"`
}

type ServerConfig struct {
	Hostname string `toml:"hostname"`
	MOTD     string `toml:"motd"`
}

type SSHConfig struct {
	Port    int    `toml:"port"`
	HostKey string `toml:"host_key"`
}

type WebConfig struct {
	Port    int    `toml:"port"`
	TLSCert string `toml:"tls_cert"`
	TLSKey  string `toml:"tls_key"`
}

type DatabaseConfig struct {
	DSN      string `toml:"dsn"`
	MaxConns int    `toml:"max_conns"`
}

type FederationConfig struct {
	ASSPPort int  `toml:"assp_port"`
	NNTPPort int  `toml:"nntp_port"`
	SMTPPort int  `toml:"smtp_port"`
	Enabled  bool `toml:"enabled"`
}

type LimitsConfig struct {
	MaxUsers    int `toml:"max_users"`
	MailPerHour int `toml:"mail_per_hour"`
	NewsPerDay  int `toml:"news_per_day"`
}

func Load(path string) (*Config, error) {
	cfg := defaults()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return cfg, nil
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Hostname: "localhost",
			MOTD:     "Welcome to alternate.sh",
		},
		SSH:      SSHConfig{Port: 2222},
		Web:      WebConfig{Port: 8080},
		Database: DatabaseConfig{MaxConns: 25},
		Limits:   LimitsConfig{MaxUsers: 500, MailPerHour: 50, NewsPerDay: 20},
	}
}
