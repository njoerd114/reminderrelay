// Package config loads and validates the ReminderRelay YAML configuration.
package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the full application configuration loaded from YAML.
type Config struct {
	// HAURL is the base URL of the Home Assistant instance (e.g. "http://homeassistant.local:8123").
	HAURL string `yaml:"ha_url"`

	// HAToken is the long-lived access token used to authenticate with Home Assistant.
	HAToken string `yaml:"ha_token"`

	// PollInterval controls how often Apple Reminders are polled for changes.
	// Minimum 10s, maximum 5m. Defaults to 30s if unset.
	PollInterval time.Duration `yaml:"poll_interval"`

	// ListMappings maps Apple Reminders list names to Home Assistant todo entity IDs.
	// Example: {"Shopping": "todo.shopping", "Work": "todo.work_tasks"}
	ListMappings map[string]string `yaml:"list_mappings"`

	// Telemetry configures optional OpenTelemetry export via OTLP gRPC.
	// Omit the block entirely to disable telemetry.
	Telemetry *TelemetryConfig `yaml:"telemetry,omitempty"`
}

// TelemetryConfig holds optional OpenTelemetry settings.
type TelemetryConfig struct {
	// OTLPEndpoint is the gRPC host:port of the OTLP collector (e.g. "localhost:4317").
	OTLPEndpoint string `yaml:"otlp_endpoint"`

	// Insecure disables TLS for the collector connection. Use for local collectors.
	Insecure bool `yaml:"insecure"`

	// ServiceName overrides the OTel service.name attribute. Defaults to "reminderrelay".
	ServiceName string `yaml:"service_name"`

	// Headers contains key-value pairs sent as gRPC metadata on every OTLP
	// request. Equivalent to the OTEL_EXPORTER_OTLP_HEADERS environment
	// variable. Use this for authentication tokens, e.g.:
	//   Authorization: "Bearer <token>"
	Headers map[string]string `yaml:"headers,omitempty"`
}

// DefaultPath returns the default config file path: ~/.config/reminderrelay/config.yaml.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".config", "reminderrelay", "config.yaml"), nil
}

// Load reads and validates the configuration file at the given path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening config file %q: %w", path, err)
	}
	defer f.Close()

	var cfg Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true) // reject unknown keys to catch typos early
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config file %q: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// validate checks that all required fields are present and well-formed.
func (c *Config) validate() error {
	if c.HAURL == "" {
		return fmt.Errorf("ha_url is required")
	}
	u, err := url.ParseRequestURI(c.HAURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("ha_url %q must be a valid http or https URL", c.HAURL)
	}

	if c.HAToken == "" {
		return fmt.Errorf("ha_token is required")
	}

	if c.PollInterval == 0 {
		c.PollInterval = 30 * time.Second
	}
	if c.PollInterval < 10*time.Second {
		return fmt.Errorf("poll_interval %v is too short (minimum 10s)", c.PollInterval)
	}
	if c.PollInterval > 5*time.Minute {
		return fmt.Errorf("poll_interval %v is too long (maximum 5m)", c.PollInterval)
	}

	if len(c.ListMappings) == 0 {
		return fmt.Errorf("list_mappings must contain at least one entry")
	}
	for list, entity := range c.ListMappings {
		if list == "" {
			return fmt.Errorf("list_mappings contains an empty Reminders list name")
		}
		if entity == "" {
			return fmt.Errorf("list_mappings[%q] has an empty HA entity ID", list)
		}
	}

	if c.Telemetry != nil {
		if c.Telemetry.OTLPEndpoint == "" {
			return fmt.Errorf("telemetry.otlp_endpoint is required when telemetry is configured")
		}
	}

	return nil
}
