package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("missing config file should not error: %v", err)
	}
	want := defaultConfig()
	if cfg != want {
		t.Errorf("expected defaults %+v, got %+v", want, cfg)
	}
	if cfg.MQTT.Broker != "" || cfg.Web.Addr != "" {
		t.Errorf("MQTT/web must be disabled by default, got %+v", cfg)
	}
}

func TestLoadConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `
pool: mypool
os_disk_device: sdz
refresh: 10s
web:
  addr: "127.0.0.1:9090"
mqtt:
  broker: "tcp://broker.local:1883"
  username: hauser
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Pool != "mypool" || cfg.OSDiskDevice != "sdz" || cfg.Refresh != "10s" {
		t.Errorf("unexpected scalar fields: %+v", cfg)
	}
	if cfg.Web.Addr != "127.0.0.1:9090" {
		t.Errorf("unexpected web addr: %+v", cfg)
	}
	if cfg.MQTT.Broker != "tcp://broker.local:1883" || cfg.MQTT.Username != "hauser" {
		t.Errorf("unexpected mqtt config: %+v", cfg)
	}
	// unset fields should keep their defaults
	if cfg.Port != "/dev/ttyS1" || cfg.Baud != 1200 {
		t.Errorf("expected unset fields to keep defaults, got %+v", cfg)
	}
	if d, err := cfg.refreshDuration(); err != nil || d.Seconds() != 10 {
		t.Errorf("expected 10s refresh, got %v, err %v", d, err)
	}
}
