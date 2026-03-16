package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server  ServerConfig  `toml:"server"`
	Daemon  DaemonConfig  `toml:"daemon"`
	Session SessionConfig `toml:"session"`
	Claude  ClaudeConfig  `toml:"claude"`
}

type ServerConfig struct {
	Port int `toml:"port"`
}

type DaemonConfig struct {
	Interval string `toml:"interval"`
}

type SessionConfig struct {
	ClaudeCommand string `toml:"claude_command"`
	CodexCommand  string `toml:"codex_command"`
	SystemPrompt  string `toml:"system_prompt"`
}

type ClaudeConfig struct {
	PermissionMode string `toml:"permission_mode"`
}

// ClaudePermissionMode returns the effective permission mode, defaulting to "bypassPermissions".
func (c ClaudeConfig) ClaudePermissionMode() string {
	if c.PermissionMode == "" {
		return "bypassPermissions"
	}
	return c.PermissionMode
}

func (d DaemonConfig) IntervalDuration() time.Duration {
	dur, err := time.ParseDuration(d.Interval)
	if err != nil {
		return 30 * time.Second
	}
	return dur
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 4321,
		},
		Daemon: DaemonConfig{
			Interval: "30s",
		},
		Session: SessionConfig{
			ClaudeCommand: "claude",
			CodexCommand:  "codex",
		},
		Claude: ClaudeConfig{
			PermissionMode: "bypassPermissions",
		},
	}
}

func configPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agmux", "config.toml"), nil
}

func Save(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return fmt.Errorf("get config path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return nil
}

func Load() (*Config, error) {
	cfg := Default()

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, nil
	}

	configPath := filepath.Join(home, ".agmux", "config.toml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(configPath, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}
