// Package haswitch exposes the LCD backlight to Home Assistant as an MQTT
// switch entity, using MQTT Discovery so it appears automatically without
// any YAML configuration on the Home Assistant side.
package haswitch

import (
	"encoding/json"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

const (
	discoveryTopic    = "homeassistant/switch/qnap_led_backlight/config"
	commandTopic      = "qnap-led/backlight/set"
	stateTopic        = "qnap-led/backlight/state"
	availabilityTopic = "qnap-led/status"
)

type discoveryPayload struct {
	Name              string          `json:"name"`
	UniqueID          string          `json:"unique_id"`
	CommandTopic      string          `json:"command_topic"`
	StateTopic        string          `json:"state_topic"`
	PayloadOn         string          `json:"payload_on"`
	PayloadOff        string          `json:"payload_off"`
	StateOn           string          `json:"state_on"`
	StateOff          string          `json:"state_off"`
	AvailabilityTopic string          `json:"availability_topic"`
	PayloadAvailable  string          `json:"payload_available"`
	PayloadNotAvail   string          `json:"payload_not_available"`
	Device            discoveryDevice `json:"device"`
}

type discoveryDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
}

// BacklightSwitch is a connected MQTT client publishing/subscribing the
// backlight switch entity.
type BacklightSwitch struct {
	client mqtt.Client
}

// Connect dials the MQTT broker, publishes discovery config + availability,
// and subscribes to the command topic. onSet is invoked with true/false
// whenever Home Assistant requests a backlight change.
func Connect(broker, username, password string, onSet func(on bool)) (*BacklightSwitch, error) {
	s := &BacklightSwitch{}

	opts := mqtt.NewClientOptions().
		AddBroker(broker).
		SetClientID("qnap-led").
		SetAutoReconnect(true).
		SetWill(availabilityTopic, "offline", 1, true).
		SetOnConnectHandler(func(c mqtt.Client) {
			if err := s.announce(c); err != nil {
				log.Printf("haswitch: discovery publish failed: %v", err)
			}
			c.Subscribe(commandTopic, 1, func(_ mqtt.Client, msg mqtt.Message) {
				onSet(string(msg.Payload()) == "ON")
			})
		})
	if username != "" {
		opts.SetUsername(username)
		opts.SetPassword(password)
	}

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		if err := token.Error(); err != nil {
			return nil, err
		}
		return nil, mqtt.ErrNotConnected
	}
	s.client = client
	return s, nil
}

func (s *BacklightSwitch) announce(c mqtt.Client) error {
	payload, err := json.Marshal(discoveryPayload{
		Name:              "QNAP LCD Backlight",
		UniqueID:          "qnap_led_backlight",
		CommandTopic:      commandTopic,
		StateTopic:        stateTopic,
		PayloadOn:         "ON",
		PayloadOff:        "OFF",
		StateOn:           "ON",
		StateOff:          "OFF",
		AvailabilityTopic: availabilityTopic,
		PayloadAvailable:  "online",
		PayloadNotAvail:   "offline",
		Device: discoveryDevice{
			Identifiers:  []string{"qnap-led"},
			Name:         "QNAP TS-670 Pro",
			Manufacturer: "QNAP",
			Model:        "TS-670 Pro",
		},
	})
	if err != nil {
		return err
	}
	c.Publish(discoveryTopic, 1, true, payload)
	c.Publish(availabilityTopic, 1, true, "online")
	return nil
}

// PublishState reports the current backlight state to Home Assistant. Call
// it whenever the backlight changes, including from the physical ENTER
// button, to keep HA's UI in sync.
func (s *BacklightSwitch) PublishState(on bool) {
	payload := "OFF"
	if on {
		payload = "ON"
	}
	s.client.Publish(stateTopic, 1, true, payload)
}

// Close marks the entity unavailable and disconnects.
func (s *BacklightSwitch) Close() {
	token := s.client.Publish(availabilityTopic, 1, true, "offline")
	token.WaitTimeout(2 * time.Second)
	s.client.Disconnect(250)
}
