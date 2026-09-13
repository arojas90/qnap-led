package main

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// defaultConfigYAML is config.example.yaml, embedded verbatim so the
// binary can write it out as a starting point on first run — see
// loadConfig. Keeping this as the single source (rather than duplicating
// its contents elsewhere, e.g. in install.sh) means the shipped defaults
// and the documented example can never drift apart.
//
//go:embed config.example.yaml
var defaultConfigYAML []byte

// Config is the on-disk shape of the optional YAML config file. Every field
// has a zero-value meaning "disabled"/"use built-in default" — see
// defaultConfig. Features gated on external configuration (MQTT, the web
// UI) only turn on when their section is actually present and non-empty in
// the file; nothing here is opt-out, everything is opt-in by presence.
type Config struct {
	Port              string `yaml:"port"`
	Baud              int    `yaml:"baud"`
	Pool              string `yaml:"pool"`
	OSDiskDevice      string `yaml:"os_disk_device"`
	Refresh           string `yaml:"refresh"`
	CustomScreensFile string `yaml:"custom_screens_file"`

	Web struct {
		Addr string `yaml:"addr"`
	} `yaml:"web"`

	MQTT struct {
		Broker   string `yaml:"broker"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"mqtt"`
}

// defaultConfig parses the embedded config.example.yaml, so the true
// built-in defaults (port, baud, pool, ...) and the documented example can
// never drift apart. Web.Addr and MQTT.* come out zero-valued (disabled)
// because those sections are commented out in the example — those
// features depend on external configuration and must only activate when
// the user actually fills them in, in the config file or via flags.
func defaultConfig() Config {
	var c Config
	if err := yaml.Unmarshal(defaultConfigYAML, &c); err != nil {
		// Can only happen if config.example.yaml itself is malformed —
		// a build-time bug, not a runtime/user error.
		panic(fmt.Sprintf("embedded config.example.yaml is invalid YAML: %v", err))
	}
	return c
}

// loadConfig starts from defaultConfig and, if path exists, overlays values
// present in the YAML file on top of it. If path doesn't exist, it's
// created from the embedded default so there's something to edit — the
// daemon still runs fine on the in-memory defaults even if that write
// fails (e.g. permission denied), since the config file is optional.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if writeErr := writeDefaultConfig(path); writeErr != nil {
				log.Printf("warning: could not write default config to %s: %v", path, writeErr)
			}
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config file %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return cfg, nil
}

func writeDefaultConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, defaultConfigYAML, 0o644)
}

func (c Config) refreshDuration() (time.Duration, error) {
	d, err := time.ParseDuration(c.Refresh)
	if err != nil {
		return 0, fmt.Errorf("invalid refresh duration %q: %w", c.Refresh, err)
	}
	return d, nil
}
