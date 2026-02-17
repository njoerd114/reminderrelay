package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf("creating temp config: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	f.Close()
	return f.Name()
}

func TestLoad_Valid(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://homeassistant.local:8123"
ha_token: "abc123"
poll_interval: 45s
list_mappings:
  Shopping: todo.shopping
  Work: todo.work_tasks
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HAURL != "http://homeassistant.local:8123" {
		t.Errorf("HAURL = %q, want %q", cfg.HAURL, "http://homeassistant.local:8123")
	}
	if cfg.HAToken != "abc123" {
		t.Errorf("HAToken = %q, want %q", cfg.HAToken, "abc123")
	}
	if cfg.PollInterval != 45*time.Second {
		t.Errorf("PollInterval = %v, want 45s", cfg.PollInterval)
	}
	if len(cfg.ListMappings) != 2 {
		t.Errorf("ListMappings len = %d, want 2", len(cfg.ListMappings))
	}
}

func TestLoad_DefaultPollInterval(t *testing.T) {
	path := writeConfig(t, `
ha_url: "https://ha.example.com"
ha_token: "token"
list_mappings:
  Shopping: todo.shopping
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v, want default 30s", cfg.PollInterval)
	}
}

func TestLoad_MissingHAURL(t *testing.T) {
	path := writeConfig(t, `
ha_token: "token"
list_mappings:
  Shopping: todo.shopping
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing ha_url, got nil")
	}
}

func TestLoad_InvalidHAURL(t *testing.T) {
	path := writeConfig(t, `
ha_url: "not-a-url"
ha_token: "token"
list_mappings:
  Shopping: todo.shopping
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid ha_url, got nil")
	}
}

func TestLoad_MissingToken(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
list_mappings:
  Shopping: todo.shopping
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing ha_token, got nil")
	}
}

func TestLoad_PollIntervalTooShort(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
ha_token: "token"
poll_interval: 5s
list_mappings:
  Shopping: todo.shopping
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for poll_interval < 10s, got nil")
	}
}

func TestLoad_PollIntervalTooLong(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
ha_token: "token"
poll_interval: 10m
list_mappings:
  Shopping: todo.shopping
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for poll_interval > 5m, got nil")
	}
}

func TestLoad_EmptyListMappings(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
ha_token: "token"
list_mappings: {}
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty list_mappings, got nil")
	}
}

func TestLoad_UnknownKey(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
ha_token: "token"
list_mappings:
  Shopping: todo.shopping
unknown_field: oops
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown config key, got nil")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestDefaultPath(t *testing.T) {
	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path == "" {
		t.Error("DefaultPath returned empty string")
	}
}

func TestLoad_TelemetryValid(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
ha_token: "token"
list_mappings:
  Shopping: todo.shopping
telemetry:
  otlp_endpoint: "localhost:4317"
  insecure: true
  service_name: "my-reminderrelay"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telemetry == nil {
		t.Fatal("expected Telemetry to be non-nil")
	}
	if cfg.Telemetry.OTLPEndpoint != "localhost:4317" {
		t.Errorf("OTLPEndpoint = %q, want %q", cfg.Telemetry.OTLPEndpoint, "localhost:4317")
	}
	if !cfg.Telemetry.Insecure {
		t.Error("Insecure = false, want true")
	}
	if cfg.Telemetry.ServiceName != "my-reminderrelay" {
		t.Errorf("ServiceName = %q, want %q", cfg.Telemetry.ServiceName, "my-reminderrelay")
	}
}

func TestLoad_TelemetryOmitted(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
ha_token: "token"
list_mappings:
  Shopping: todo.shopping
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telemetry != nil {
		t.Error("expected Telemetry to be nil when block is omitted")
	}
}

func TestLoad_TelemetryMissingEndpoint(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
ha_token: "token"
list_mappings:
  Shopping: todo.shopping
telemetry:
  insecure: true
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for telemetry missing otlp_endpoint, got nil")
	}
}

func TestLoad_TelemetryHeaders(t *testing.T) {
	path := writeConfig(t, `
ha_url: "http://ha.local:8123"
ha_token: "token"
list_mappings:
  Shopping: todo.shopping
telemetry:
  otlp_endpoint: "otelcol.example.com:4317"
  headers:
    Authorization: "Bearer secret"
    x-dataset: "test"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Telemetry.Headers) != 2 {
		t.Fatalf("Headers len = %d, want 2", len(cfg.Telemetry.Headers))
	}
	if cfg.Telemetry.Headers["Authorization"] != "Bearer secret" {
		t.Errorf("Authorization header = %q, want %q", cfg.Telemetry.Headers["Authorization"], "Bearer secret")
	}
	if cfg.Telemetry.Headers["x-dataset"] != "test" {
		t.Errorf("x-dataset header = %q, want %q", cfg.Telemetry.Headers["x-dataset"], "test")
	}
}
