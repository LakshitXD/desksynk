package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/LakshitXD/desksynk/internal/proto"
)

// hyprland talks to the compositor's IPC socket. Wayland gives clients no way
// to read the global cursor position, so this is how edge crossing is seen.
type hyprland struct {
	socket      string
	monitors    []monitor
	monitorsAt  time.Time
	lastQueryAt time.Time
}

type monitor struct {
	Name      string  `json:"name"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Width     float64 `json:"width"`
	Height    float64 `json:"height"`
	Scale     float64 `json:"scale"`
	Transform int     `json:"transform"`
	Disabled  bool    `json:"disabled"`
}

// logical returns the monitor's size in layout coordinates.
func (m monitor) logical() (w, h float64) {
	w, h = m.Width, m.Height
	if m.Transform%2 == 1 {
		w, h = h, w
	}
	if m.Scale > 0 {
		w, h = w/m.Scale, h/m.Scale
	}
	return w, h
}

func (m monitor) contains(x, y float64) bool {
	w, h := m.logical()
	return x >= m.X && x < m.X+w && y >= m.Y && y < m.Y+h
}

func findHyprSocket() (string, error) {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); sig != "" {
		for _, p := range []string{filepath.Join(rt, "hypr", sig, ".socket.sock"), filepath.Join("/tmp/hypr", sig, ".socket.sock")} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	// Launched outside the Hyprland session (e.g. a systemd user unit
	// without the env imported): take the newest instance.
	matches, _ := filepath.Glob(filepath.Join(rt, "hypr", "*", ".socket.sock"))
	if len(matches) == 0 {
		return "", errors.New("Hyprland IPC socket not found; is Hyprland running?")
	}
	sort.Slice(matches, func(i, j int) bool {
		a, _ := os.Stat(matches[i])
		b, _ := os.Stat(matches[j])
		return a.ModTime().After(b.ModTime())
	})
	return matches[0], nil
}

func (h *hyprland) request(cmd string) ([]byte, error) {
	c, err := net.DialTimeout("unix", h.socket, time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second))
	if _, err := c.Write([]byte(cmd)); err != nil {
		return nil, err
	}
	return io.ReadAll(c)
}

func (h *hyprland) cursor() (x, y float64, err error) {
	out, err := h.request("cursorpos")
	if err != nil {
		return 0, 0, err
	}
	_, err = fmt.Sscanf(string(out), "%g, %g", &x, &y)
	return x, y, err
}

func (h *hyprland) refreshMonitors() error {
	if time.Since(h.monitorsAt) < 2*time.Second && h.monitors != nil {
		return nil
	}
	out, err := h.request("j/monitors")
	if err != nil {
		return err
	}
	var ms []monitor
	if err := json.Unmarshal(out, &ms); err != nil {
		return err
	}
	h.monitors = h.monitors[:0]
	for _, m := range ms {
		if !m.Disabled {
			h.monitors = append(h.monitors, m)
		}
	}
	h.monitorsAt = time.Now()
	return nil
}

func (h *hyprland) monitorAt(x, y float64) (monitor, bool) {
	for _, m := range h.monitors {
		if m.contains(x, y) {
			return m, true
		}
	}
	return monitor{}, false
}

// crossing reports whether the cursor sits on the outer edge of the desktop on
// side, and where along that edge (0..1). Calls are rate limited because a
// 1000 Hz mouse would otherwise hit the socket 1000 times a second.
func (h *hyprland) crossing(side proto.Edge) (frac float64, ok bool) {
	if time.Since(h.lastQueryAt) < 8*time.Millisecond {
		return 0, false
	}
	h.lastQueryAt = time.Now()
	x, y, err := h.cursor()
	if err != nil || h.refreshMonitors() != nil {
		return 0, false
	}
	m, found := h.monitorAt(x, y)
	if !found {
		return 0, false
	}
	w, ht := m.logical()
	const slack = 1.5
	var at bool
	var px, py float64
	switch side {
	case proto.Left:
		at, px, py, frac = x <= m.X+slack, m.X-2, y, (y-m.Y)/ht
	case proto.Right:
		at, px, py, frac = x >= m.X+w-1-slack, m.X+w+1, y, (y-m.Y)/ht
	case proto.Top:
		at, px, py, frac = y <= m.Y+slack, x, m.Y-2, (x-m.X)/w
	case proto.Bottom:
		at, px, py, frac = y >= m.Y+ht-1-slack, x, m.Y+ht+1, (x-m.X)/w
	}
	if !at {
		return 0, false
	}
	if _, neighbour := h.monitorAt(px, py); neighbour {
		return 0, false
	}
	return frac, true
}

// warpToEdge puts the cursor just inside the side edge at frac along it, on
// the outermost monitor of that side.
func (h *hyprland) warpToEdge(side proto.Edge, frac float64) {
	h.monitorsAt = time.Time{}
	if h.refreshMonitors() != nil || len(h.monitors) == 0 {
		return
	}
	outer := func(m monitor) float64 {
		w, ht := m.logical()
		switch side {
		case proto.Left:
			return -m.X
		case proto.Right:
			return m.X + w
		case proto.Top:
			return -m.Y
		default:
			return m.Y + ht
		}
	}
	best := h.monitors[0]
	for _, m := range h.monitors[1:] {
		if outer(m) > outer(best) {
			best = m
		}
	}
	w, ht := best.logical()
	const inset = 3
	var x, y float64
	switch side {
	case proto.Left:
		x, y = best.X+inset, best.Y+frac*ht
	case proto.Right:
		x, y = best.X+w-1-inset, best.Y+frac*ht
	case proto.Top:
		x, y = best.X+frac*w, best.Y+inset
	case proto.Bottom:
		x, y = best.X+frac*w, best.Y+ht-1-inset
	}
	h.moveCursor(int(x), int(y))
}

// moveCursor speaks both dispatch dialects: hyprlang configs take
// "movecursor X Y", Lua configs (0.55+) take hl.dsp.cursor.move{x, y}.
func (h *hyprland) moveCursor(x, y int) {
	for _, cmd := range []string{
		fmt.Sprintf("dispatch hl.dsp.cursor.move({x = %d, y = %d})", x, y),
		fmt.Sprintf("dispatch movecursor %d %d", x, y),
	} {
		out, err := h.request(cmd)
		if err == nil && strings.TrimSpace(string(out)) == "ok" {
			return
		}
	}
	logf("could not move the cursor through Hyprland IPC")
}
