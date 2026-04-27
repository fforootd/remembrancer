package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	User   UserConfig   `yaml:"user"`
	Paths  PathConfig   `yaml:"paths"`
	SQLite SQLiteConfig `yaml:"sqlite"`
	Ingest IngestConfig `yaml:"ingest"`
	LLM    LLMConfig    `yaml:"llm"`
}

type ServerConfig struct {
	Bind string `yaml:"bind"`
}

type UserConfig struct {
	ID          string `yaml:"id"`
	DisplayName string `yaml:"display_name"`
}

type PathConfig struct {
	Runtime string `yaml:"runtime"`
	Archive string `yaml:"archive"`
	Inbox   string `yaml:"inbox"`
}

type SQLiteConfig struct {
	Path string `yaml:"path"`
}

type IngestConfig struct {
	Enabled        bool          `yaml:"enabled"`
	ScanInterval   time.Duration `yaml:"scan_interval"`
	SettleDuration time.Duration `yaml:"settle_duration"`
	Workers        int           `yaml:"workers"`
	ExtractTimeout time.Duration `yaml:"extract_timeout"`
	MaxAttempts    int           `yaml:"max_attempts"`
}

type LLMConfig struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
	Model   string `yaml:"model"`
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			Bind: "127.0.0.1:8787",
		},
		User: UserConfig{
			ID:          "florian",
			DisplayName: "Florian",
		},
		Paths: PathConfig{
			Runtime: ".local/runtime",
			Archive: ".local/archive",
			Inbox:   ".local/inbox",
		},
		SQLite: SQLiteConfig{
			Path: ".local/runtime/users/florian/main.sqlite",
		},
		Ingest: IngestConfig{
			Enabled:        true,
			ScanInterval:   30 * time.Second,
			SettleDuration: 10 * time.Second,
			Workers:        2,
			ExtractTimeout: 2 * time.Minute,
			MaxAttempts:    3,
		},
		LLM: LLMConfig{
			Enabled: false,
			BaseURL: "http://127.0.0.1:11434/v1",
			Model:   "",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, cfg.Validate()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	if err := validateLoopbackBind(cfg.Server.Bind); err != nil {
		return fmt.Errorf("server.bind: %w", err)
	}
	if cfg.User.ID == "" {
		return errors.New("user.id is required")
	}
	if cfg.User.DisplayName == "" {
		return errors.New("user.display_name is required")
	}
	if err := validatePath("paths.runtime", cfg.Paths.Runtime); err != nil {
		return err
	}
	if err := validatePath("paths.archive", cfg.Paths.Archive); err != nil {
		return err
	}
	if err := validatePath("paths.inbox", cfg.Paths.Inbox); err != nil {
		return err
	}
	if err := validatePath("sqlite.path", cfg.SQLite.Path); err != nil {
		return err
	}
	if cfg.Ingest.ScanInterval <= 0 {
		return errors.New("ingest.scan_interval must be positive")
	}
	if cfg.Ingest.SettleDuration < 0 {
		return errors.New("ingest.settle_duration must not be negative")
	}
	if cfg.Ingest.Workers < 1 {
		return errors.New("ingest.workers must be at least 1")
	}
	if cfg.Ingest.ExtractTimeout <= 0 {
		return errors.New("ingest.extract_timeout must be positive")
	}
	if cfg.Ingest.MaxAttempts < 1 {
		return errors.New("ingest.max_attempts must be at least 1")
	}
	if cfg.LLM.Enabled && cfg.LLM.BaseURL == "" {
		return errors.New("llm.base_url is required when llm.enabled is true")
	}
	return nil
}

func EnsureLocalPaths(cfg Config) error {
	for _, path := range []string{
		cfg.Paths.Runtime,
		cfg.Paths.Archive,
		cfg.Paths.Inbox,
		filepath.Dir(cfg.SQLite.Path),
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func validateLoopbackBind(bind string) error {
	if bind == "" {
		return errors.New("is required")
	}

	host, port, err := net.SplitHostPort(bind)
	if err != nil {
		return err
	}
	if port == "" {
		return errors.New("port is required")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("invalid port: %w", err)
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if !ip.IsLoopback() {
			return errors.New("must bind to a loopback address until authentication exists")
		}
		return nil
	}

	if host != "localhost" {
		return errors.New("must bind to localhost or a loopback address until authentication exists")
	}
	return nil
}

func validatePath(name, path string) error {
	if path == "" {
		return fmt.Errorf("%s is required", name)
	}
	if filepath.Clean(path) == "." {
		return fmt.Errorf("%s must not point at the working directory", name)
	}
	return nil
}
