package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load default config: %v", err)
	}

	if cfg.Server.Bind != "127.0.0.1:8787" {
		t.Fatalf("Server.Bind = %q", cfg.Server.Bind)
	}
	if cfg.User.ID != "florian" {
		t.Fatalf("User.ID = %q", cfg.User.ID)
	}
	if cfg.LLM.Enabled {
		t.Fatal("LLM should be disabled by default")
	}
}

func TestLoadExplicitConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
server:
  bind: "localhost:9999"
user:
  id: "florian"
  display_name: "Florian Test"
paths:
  runtime: "runtime"
  archive: "archive"
  inbox: "inbox"
sqlite:
  path: "runtime/users/florian/main.sqlite"
llm:
  enabled: true
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load explicit config: %v", err)
	}

	if cfg.Server.Bind != "localhost:9999" {
		t.Fatalf("Server.Bind = %q", cfg.Server.Bind)
	}
	if cfg.User.DisplayName != "Florian Test" {
		t.Fatalf("User.DisplayName = %q", cfg.User.DisplayName)
	}
	if !cfg.LLM.Enabled {
		t.Fatal("LLM should be enabled by explicit config")
	}
}

func TestLoadRejectsInvalidBind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
server:
  bind: "0.0.0.0:8787"
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected invalid bind error")
	}
}

func TestLoadRejectsEmptyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
paths:
  runtime: ""
`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config fixture: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("expected empty path error")
	}
}

func TestEnsureLocalPaths(t *testing.T) {
	root := t.TempDir()
	cfg := Default()
	cfg.Paths.Runtime = filepath.Join(root, "runtime")
	cfg.Paths.Archive = filepath.Join(root, "archive")
	cfg.Paths.Inbox = filepath.Join(root, "inbox")
	cfg.SQLite.Path = filepath.Join(root, "runtime", "users", "florian", "main.sqlite")

	if err := EnsureLocalPaths(cfg); err != nil {
		t.Fatalf("EnsureLocalPaths: %v", err)
	}

	for _, path := range []string{
		cfg.Paths.Runtime,
		cfg.Paths.Archive,
		cfg.Paths.Inbox,
		filepath.Dir(cfg.SQLite.Path),
	} {
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			t.Fatalf("expected directory %s, got info=%v err=%v", path, info, err)
		}
	}
}
