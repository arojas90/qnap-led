// Package customscreens lets users define ad hoc LCD screens backed by an
// arbitrary shell command, managed at runtime (e.g. from the web UI) and
// persisted to a JSON file so they survive a restart.
package customscreens

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Screen is one user-defined LCD screen.
//
// If Command's output is a single line, it becomes the LCD's second row
// with Name as the label on the first row. If it's two or more lines, the
// first two lines are used directly for both rows (Name is then unused for
// display, only for identifying the screen in the web UI).
type Screen struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

const execTimeout = 5 * time.Second

// Render runs the screen's command and returns up to two 16-char LCD rows.
func (s Screen) Render() (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", s.Command).Output()
	if err != nil {
		return s.Name, "cmd error"
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return s.Name, "(no output)"
	}
	lines := strings.SplitN(trimmed, "\n", 2)
	if len(lines) == 1 {
		return s.Name, lines[0]
	}
	return lines[0], lines[1]
}

// Registry is a thread-safe, disk-persisted collection of custom screens.
type Registry struct {
	mu      sync.RWMutex
	path    string
	screens []Screen
}

// Open loads the registry from path, creating its parent directory (but not
// the file itself) if needed. A missing file is treated as an empty registry.
func Open(path string) (*Registry, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	r := &Registry{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, &r.screens); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return r, nil
}

func (r *Registry) save() error {
	data, err := json.MarshalIndent(r.screens, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.path, data, 0o600)
}

// List returns a snapshot of the current custom screens, in insertion order.
func (r *Registry) List() []Screen {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Screen, len(r.screens))
	copy(out, r.screens)
	return out
}

// Add validates and appends a new screen, persisting the registry.
func (r *Registry) Add(name, command string) (Screen, error) {
	name = strings.TrimSpace(name)
	command = strings.TrimSpace(command)
	if name == "" {
		return Screen{}, fmt.Errorf("name is required")
	}
	if command == "" {
		return Screen{}, fmt.Errorf("command is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	s := Screen{
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()),
		Name:    name,
		Command: command,
	}
	r.screens = append(r.screens, s)
	if err := r.save(); err != nil {
		r.screens = r.screens[:len(r.screens)-1]
		return Screen{}, err
	}
	return s, nil
}

// Delete removes the screen with the given ID, persisting the registry.
func (r *Registry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, s := range r.screens {
		if s.ID != id {
			continue
		}
		original := r.screens
		r.screens = append(append([]Screen{}, r.screens[:i]...), r.screens[i+1:]...)
		if err := r.save(); err != nil {
			r.screens = original
			return err
		}
		return nil
	}
	return fmt.Errorf("screen %q not found", id)
}
