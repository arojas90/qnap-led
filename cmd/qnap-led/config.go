package main

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

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

func defaultConfig() Config {
	var c Config
	c.Port = "/dev/ttyS1"
	c.Baud = 1200
	c.Pool = "tank"
	c.OSDiskDevice = "sdg"
	c.Refresh = "5s"
	c.CustomScreensFile = "/etc/qnap-led/custom_screens.json"
	// Web.Addr and MQTT.* are left zero-valued (disabled) by design: those
	// features depend on external configuration and must only activate
	// when the user actually fills them in, in the config file or via flags.
	return c
}

// loadConfig starts from defaultConfig and, if path exists, overlays values
// present in the YAML file on top of it. A missing file is not an error —
// the config file is entirely optional, flags alone still work.
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("reading config file %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing config file %s: %w", path, err)
	}
	return cfg, nil
}

func (c Config) refreshDuration() (time.Duration, error) {
	d, err := time.ParseDuration(c.Refresh)
	if err != nil {
		return 0, fmt.Errorf("invalid refresh duration %q: %w", c.Refresh, err)
	}
	return d, nil
}
