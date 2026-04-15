package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type RoleTemplate struct {
	Name         string `toml:"name" json:"name"`
	Provider     string `toml:"provider" json:"provider"`
	Model        string `toml:"model,omitempty" json:"model,omitempty"`
	SystemPrompt string `toml:"systemPrompt" json:"systemPrompt"`
}

type PromptTemplate struct {
	Name     string `toml:"name" json:"name"`
	Prompt   string `toml:"prompt" json:"prompt"`
	Category string `toml:"category" json:"category"`
}

type Config struct {
	Server          ServerConfig     `toml:"server"`
	Daemon          DaemonConfig     `toml:"daemon"`
	Session         SessionConfig    `toml:"session"`
	Claude          ClaudeConfig     `toml:"claude"`
	GitHub          GitHubConfig     `toml:"github"`
	DevMode         bool             `toml:"dev_mode"`
	Templates       []RoleTemplate   `toml:"templates" json:"templates"`
	PromptTemplates []PromptTemplate `toml:"prompt_templates" json:"prompt_templates"`
}

type ServerConfig struct {
	Port        int    `toml:"port"`
	FrontendDir string `toml:"frontend_dir"`
}

type DaemonConfig struct {
	Interval                          string `toml:"interval"`
	BackgroundTaskNotificationInterval string `toml:"background_task_notification_interval"`
}

type SessionConfig struct {
	ClaudeCommand string `toml:"claude_command"`
	CodexCommand  string `toml:"codex_command"`
	SystemPrompt  string `toml:"system_prompt"`
	DefaultRole   string `toml:"default_role" json:"default_role,omitempty"`
	DefaultModel  string `toml:"default_model" json:"default_model,omitempty"`
}

// DefaultPermissionMode is the default Claude CLI permission mode.
const DefaultPermissionMode = "bypassPermissions"

// validPermissionModes lists all valid Claude CLI permission modes.
var validPermissionModes = map[string]bool{
	"default":           true,
	"acceptEdits":       true,
	"plan":              true,
	"dontAsk":           true,
	"bypassPermissions": true,
	"auto":              true,
}

// IsValidPermissionMode returns true if the given mode is a valid Claude CLI permission mode.
func IsValidPermissionMode(mode string) bool {
	return validPermissionModes[mode]
}

type ClaudeConfig struct {
	PermissionMode string `toml:"permission_mode"`
}

type GitHubConfig struct {
	WebhookSecret string `toml:"webhook_secret"`
}

// ClaudePermissionMode returns the effective permission mode, defaulting to DefaultPermissionMode.
func (c ClaudeConfig) ClaudePermissionMode() string {
	if c.PermissionMode == "" {
		return DefaultPermissionMode
	}
	if !IsValidPermissionMode(c.PermissionMode) {
		return DefaultPermissionMode
	}
	return c.PermissionMode
}

func Default() *Config {
	return &Config{
		Server: ServerConfig{
			Port: 4321,
		},
		Daemon: DaemonConfig{
			Interval:                          "30s",
			BackgroundTaskNotificationInterval: "30m",
		},
		Session: SessionConfig{
			ClaudeCommand: "claude",
			CodexCommand:  "codex",
		},
		Claude: ClaudeConfig{
			PermissionMode: DefaultPermissionMode,
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

// ConfigPath returns the path to the config file.
func ConfigPath() (string, error) {
	return configPath()
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
