// Package panel implements the wire protocol for the QNAP A125 front panel
// (LCD text, backlight, and the ENTER/SELECT buttons) over a serial port.
package panel

import (
	"bytes"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	cmdPrefix      byte = 0x4D
	cmdWriteLine   byte = 0x0C
	cmdBacklight   byte = 0x5E
	lineHeaderByte byte = 0x20

	// LCDColumns is the fixed width of each of the panel's two text rows.
	LCDColumns = 16
)

var (
	btnEnter  = []byte{0x53, 0x05, 0x00, 0x01}
	btnSelect = []byte{0x53, 0x05, 0x00, 0x02}
)

// Panel is a thread-safe handle to the front panel's serial connection.
// Reads and writes share one fd, so button polling and display updates must
// go through the same instance, guarded by an internal mutex.
type Panel struct {
	port serial.Port
	mu   sync.Mutex
}

// Open configures and opens the serial connection to the front panel.
func Open(device string, baud int) (*Panel, error) {
	port, err := serial.Open(device, &serial.Mode{BaudRate: baud})
	if err != nil {
		return nil, err
	}
	// Short read timeout keeps the lock from starving writers while
	// ReadButtons blocks waiting for the next byte.
	if err := port.SetReadTimeout(100 * time.Millisecond); err != nil {
		port.Close()
		return nil, err
	}
	return &Panel{port: port}, nil
}

func padOrTruncate(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

// WriteLine writes one 16-character row (0 = top, 1 = bottom).
func (p *Panel) WriteLine(lineIndex int, text string) error {
	if lineIndex != 0 && lineIndex != 1 {
		panic("panel: lineIndex must be 0 or 1")
	}
	payload := make([]byte, 0, 4+LCDColumns)
	payload = append(payload, cmdPrefix, cmdWriteLine, byte(lineIndex), lineHeaderByte)
	payload = append(payload, []byte(padOrTruncate(text, LCDColumns))...)

	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.port.Write(payload)
	return err
}

// WriteScreen writes both rows of a two-line screen.
func (p *Panel) WriteScreen(line1, line2 string) error {
	if err := p.WriteLine(0, line1); err != nil {
		return err
	}
	return p.WriteLine(1, line2)
}

// SetBacklight turns the LCD backlight on or off.
func (p *Panel) SetBacklight(on bool) error {
	var b byte
	if on {
		b = 0x01
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	_, err := p.port.Write([]byte{cmdPrefix, cmdBacklight, b})
	return err
}

// ReadButtons blocks, polling for ENTER/SELECT button packets, until stop is
// closed. Call it from a dedicated goroutine. onEnter/onSelect fire once per
// recognized 4-byte packet.
func (p *Panel) ReadButtons(onEnter, onSelect func(), stop <-chan struct{}) {
	buf := make([]byte, 0, 4)
	tmp := make([]byte, 1)
	for {
		select {
		case <-stop:
			return
		default:
		}

		p.mu.Lock()
		n, err := p.port.Read(tmp)
		p.mu.Unlock()
		if err != nil || n == 0 {
			continue
		}

		buf = append(buf, tmp[0])
		if len(buf) > 4 {
			buf = buf[len(buf)-4:]
		}
		if len(buf) < 4 {
			continue
		}
		switch {
		case bytes.Equal(buf, btnEnter):
			onEnter()
			buf = buf[:0]
		case bytes.Equal(buf, btnSelect):
			onSelect()
			buf = buf[:0]
		}
	}
}

// Close releases the underlying serial port.
func (p *Panel) Close() error {
	return p.port.Close()
}
