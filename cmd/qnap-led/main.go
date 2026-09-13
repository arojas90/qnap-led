// Command qnap-led drives the QNAP front panel LCD: it rotates through
// system info screens, and lets the physical buttons, a web UI, and
// (optionally) Home Assistant control it.
//
// Configuration comes from an optional YAML file (--config, default
// /etc/qnap-led/config.yaml) with CLI flags layered on top as overrides —
// see config.go. Features that depend on external configuration (MQTT, the
// web UI) are disabled unless their section is actually filled in.
//
// SELECT advances to the next screen. ENTER toggles the backlight; if MQTT
// is enabled, the same toggle is exposed to Home Assistant as a switch
// entity via MQTT Discovery. If the web UI is enabled, it lets you add
// custom screens backed by arbitrary shell commands.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/arojas/qnap-led/internal/customscreens"
	"github.com/arojas/qnap-led/internal/haswitch"
	"github.com/arojas/qnap-led/internal/panel"
	"github.com/arojas/qnap-led/internal/screens"
	"github.com/arojas/qnap-led/internal/webui"
)

func main() {
	configPath := flag.String("config", "/etc/qnap-led/config.yaml", "path to YAML config file (optional; missing file is not an error)")
	port := flag.String("port", "", "serial device for the front panel (overrides config file)")
	baud := flag.Int("baud", 0, "serial baud rate (overrides config file)")
	pool := flag.String("pool", "", "ZFS pool name to report on (overrides config file)")
	osDiskDevice := flag.String("os-disk-device", "", "block device without /dev/ prefix to read OS disk temperature from (overrides config file)")
	refresh := flag.String("refresh", "", `how often to refresh the current screen, e.g. "5s" (overrides config file)`)
	mqttBroker := flag.String("mqtt-broker", "", `MQTT broker URL, e.g. "tcp://localhost:1883" (overrides config file; enables Home Assistant control when set)`)
	mqttUser := flag.String("mqtt-user", "", "MQTT username (overrides config file)")
	mqttPass := flag.String("mqtt-pass", "", "MQTT password (overrides config file)")
	webAddr := flag.String("web-addr", "", `address for the custom-screens web UI, e.g. "127.0.0.1:8080" (overrides config file; enables it when set — see README security note before binding beyond localhost)`)
	customScreensFile := flag.String("custom-screens-file", "", "where custom screens added via the web UI are persisted (overrides config file)")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			cfg.Port = *port
		case "baud":
			cfg.Baud = *baud
		case "pool":
			cfg.Pool = *pool
		case "os-disk-device":
			cfg.OSDiskDevice = *osDiskDevice
		case "refresh":
			cfg.Refresh = *refresh
		case "mqtt-broker":
			cfg.MQTT.Broker = *mqttBroker
		case "mqtt-user":
			cfg.MQTT.Username = *mqttUser
		case "mqtt-pass":
			cfg.MQTT.Password = *mqttPass
		case "web-addr":
			cfg.Web.Addr = *webAddr
		case "custom-screens-file":
			cfg.CustomScreensFile = *customScreensFile
		}
	})

	refreshInterval, err := cfg.refreshDuration()
	if err != nil {
		log.Fatal(err)
	}

	p, err := panel.Open(cfg.Port, cfg.Baud)
	if err != nil {
		log.Fatalf("opening panel on %s: %v", cfg.Port, err)
	}
	defer p.Close()

	scrs := []screens.Screen{
		screens.IP,
		screens.NetworkUsage,
		screens.CPUUsage,
		screens.CPUTemp,
		screens.RAMUsage,
		screens.SwapUsage,
		screens.Uptime,
		screens.LoadAverage,
		screens.DockerContainers,
		screens.OSDiskUsage,
		screens.OSDiskTemp(cfg.OSDiskDevice),
		screens.Zpool(cfg.Pool),
		screens.ZpoolHealth(cfg.Pool),
		screens.ZpoolIO(cfg.Pool),
		screens.DriveTemps,
		screens.HottestDrive,
	}

	registry, err := customscreens.Open(cfg.CustomScreensFile)
	if err != nil {
		log.Fatalf("opening custom screens registry: %v", err)
	}

	var (
		mu          sync.Mutex
		index       int
		backlightOn = true
		ha          *haswitch.BacklightSwitch
	)

	setBacklight := func(on bool) {
		mu.Lock()
		backlightOn = on
		mu.Unlock()
		if err := p.SetBacklight(on); err != nil {
			log.Printf("backlight: %v", err)
		}
		if ha != nil {
			ha.PublishState(on)
		}
	}

	render := func() {
		custom := registry.List()
		total := len(scrs) + len(custom)
		if total == 0 {
			return
		}
		mu.Lock()
		idx := index % total
		mu.Unlock()

		var line1, line2 string
		if idx < len(scrs) {
			line1, line2 = scrs[idx]()
		} else {
			line1, line2 = custom[idx-len(scrs)].Render()
		}
		if err := p.WriteScreen(line1, line2); err != nil {
			log.Printf("write screen: %v", err)
		}
	}

	onSelect := func() {
		total := len(scrs) + len(registry.List())
		mu.Lock()
		if total > 0 {
			index = (index + 1) % total
		}
		mu.Unlock()
		render()
	}

	onEnter := func() {
		mu.Lock()
		next := !backlightOn
		mu.Unlock()
		setBacklight(next)
	}

	if cfg.MQTT.Broker != "" {
		ha, err = haswitch.Connect(cfg.MQTT.Broker, cfg.MQTT.Username, cfg.MQTT.Password, setBacklight)
		if err != nil {
			log.Fatalf("mqtt connect: %v", err)
		}
		defer ha.Close()
		ha.PublishState(backlightOn)
	}

	if cfg.Web.Addr != "" {
		srv := webui.New(registry)
		go func() {
			if err := srv.ListenAndServe(cfg.Web.Addr); err != nil {
				log.Printf("web UI stopped: %v", err)
			}
		}()
	}

	stop := make(chan struct{})
	go p.ReadButtons(onEnter, onSelect, stop)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	render()
	for {
		select {
		case <-ticker.C:
			render()
		case <-sigCh:
			close(stop)
			return
		}
	}
}
