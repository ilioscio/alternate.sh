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
	Email      EmailConfig      `toml:"email"`
	Federation FederationConfig `toml:"federation"`
	Calls      CallsConfig      `toml:"calls"`
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
	// PublicURL is the externally reachable base URL, used to build links in
	// emails (e.g. account-confirmation URLs). No trailing slash.
	PublicURL string `toml:"public_url"`
}

// EmailConfig configures transactional email (SMTP submission). Enabled=false
// disables sending (useful for dev); signup will refuse to run without it.
type EmailConfig struct {
	Enabled      bool   `toml:"enabled"`
	Host         string `toml:"host"`          // SMTP submission host
	Port         int    `toml:"port"`          // usually 587 (STARTTLS)
	Username     string `toml:"username"`      // SMTP auth user; empty = no auth (dev catchers)
	From         string `toml:"from"`          // envelope + header From address
	FromName     string `toml:"from_name"`     // optional display name
	PasswordFile string `toml:"password_file"` // path to secret (e.g. agenix); read at send time
	// ImplicitTLS forces SMTPS (TLS from first byte). Auto-assumed for port 465.
	// Leave false for STARTTLS submission (587).
	ImplicitTLS bool `toml:"implicit_tls"`
	// SkipTLSVerify disables certificate verification. TEST/localhost only.
	SkipTLSVerify bool `toml:"skip_tls_verify"`
}

type DatabaseConfig struct {
	DSN      string `toml:"dsn"`
	MaxConns int    `toml:"max_conns"`
}

// FederationConfig configures node-to-node federation. Everything (presence,
// finger, mail/news sync, talk relay, future A/V) runs over one ASSP port.
type FederationConfig struct {
	ASSPPort int  `toml:"assp_port"`
	Enabled  bool `toml:"enabled"`
}

// CallsConfig configures the live A/V layer (DESIGN.md §9). Width/Height/FPS
// are this node's video ceiling: local calls use them directly, and
// cross-node negotiation clamps a caller's proposal to them (never upward).
type CallsConfig struct {
	Enabled bool `toml:"enabled"`
	Width   int  `toml:"width"`  // pixels; multiple of 8
	Height  int  `toml:"height"` // pixels
	FPS     int  `toml:"fps"`
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
		Email:      EmailConfig{Port: 465, From: "noreply@ilios.dev"},
		Federation: FederationConfig{ASSPPort: 4119},
		Calls:      CallsConfig{Enabled: true, Width: 128, Height: 96, FPS: 24},
		Limits:     LimitsConfig{MaxUsers: 500, MailPerHour: 50, NewsPerDay: 20},
	}
}
