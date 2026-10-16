//go:build e2e

package main

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"

	"github.com/LakshitXD/desksynk/internal/proto"
)

// Drives the real server on a live Hyprland session through a virtual uinput
// mouse. Run with: go test -tags e2e -run E2E -v .
// It grabs only its own virtual mouse; the cursor jumps around for ~2s.

const (
	uiSetEvBit   = 0x40045564
	uiSetKeyBit  = 0x40045565
	uiSetRelBit  = 0x40045566
	uiDevCreate  = 0x5501
	uiDevDestroy = 0x5502
)

func virtualMouse(t *testing.T) *os.File {
	f, err := os.OpenFile("/dev/uinput", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][2]uintptr{{uiSetEvBit, evKey}, {uiSetEvBit, evRel}, {uiSetKeyBit, btnLeft}, {uiSetRelBit, relX}, {uiSetRelBit, relY}} {
		if err := ioctl(f, c[0], c[1]); err != nil {
			t.Fatal(err)
		}
	}
	dev := make([]byte, 1116)
	copy(dev, "desksynk-e2e-mouse")
	binary.LittleEndian.PutUint16(dev[80:], 0x03) // BUS_USB
	if _, err := f.Write(dev); err != nil {
		t.Fatal(err)
	}
	if err := ioctl(f, uiDevCreate, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ioctl(f, uiDevDestroy, 0); f.Close() })
	return f
}

func emit(f *os.File, typ, code uint16, v int32) {
	b := make([]byte, inputEventSize)
	binary.LittleEndian.PutUint16(b[16:], typ)
	binary.LittleEndian.PutUint16(b[18:], code)
	binary.LittleEndian.PutUint32(b[20:], uint32(v))
	f.Write(b)
}

func move(f *os.File, dx int32) {
	emit(f, evRel, relX, dx)
	emit(f, evSyn, 0, 0)
}

func TestE2EEdgeCrossing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	mouse := virtualMouse(t)
	time.Sleep(300 * time.Millisecond)

	go func() {
		if err := runServer(serverOpts{listen: "127.0.0.1:24899", side: "left", noClipboard: true, onlyDevice: "desksynk-e2e-mouse"}); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(500 * time.Millisecond)

	cfg, _, err := tlsConfig("client")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := net.Dial("tcp4", "127.0.0.1:24899")
	if err != nil {
		t.Fatal(err)
	}
	c := tls.Client(raw, cfg)
	defer c.Close()
	out := proto.NewSender(c, false)
	out.Send(proto.Hello{Version: proto.Version})
	msgs := make(chan proto.Msg, 1024)
	go func() {
		r := bufio.NewReader(c)
		for {
			m, err := proto.Read(r)
			if err != nil {
				close(msgs)
				return
			}
			switch m.(type) {
			case proto.Ping:
				out.Send(proto.Pong{})
				continue
			case proto.Motion:
				out.Send(proto.MotionAck{})
			}
			msgs <- m
		}
	}()

	h := &hyprland{}
	if h.socket, err = findHyprSocket(); err != nil {
		t.Fatal(err)
	}
	h.refreshMonitors()
	start := h.monitors[0]
	for _, m := range h.monitors {
		if m.X < start.X {
			start = m
		}
	}
	w, ht := start.logical()
	h.moveCursor(int(start.X+w/2), int(start.Y+ht/2))
	time.Sleep(100 * time.Millisecond)

	var entered proto.Enter
	deadline := time.After(5 * time.Second)
waitEnter:
	for {
		move(mouse, -40)
		select {
		case m := <-msgs:
			if e, ok := m.(proto.Enter); ok {
				entered = e
				break waitEnter
			}
		case <-deadline:
			t.Fatal("never received Enter after pushing past the left edge")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if entered.Edge != proto.Right {
		t.Fatalf("Mac on the left should be entered at its right edge, got %v", entered.Edge)
	}
	t.Logf("entered at %v frac %.2f", entered.Edge, entered.Frac)

	beforeX, _, _ := h.cursor()
	for range 20 {
		move(mouse, -7)
		time.Sleep(2 * time.Millisecond)
	}
	var sum int32
	collect := time.After(300 * time.Millisecond)
gather:
	for {
		select {
		case m := <-msgs:
			if mo, ok := m.(proto.Motion); ok {
				sum += mo.DX
			}
		case <-collect:
			break gather
		}
	}
	afterX, _, _ := h.cursor()
	if sum != -140 {
		t.Fatalf("forwarded motion sum %d, want -140", sum)
	}
	if afterX-beforeX < -20 {
		t.Fatalf("local cursor moved %v -> %v while grabbed", beforeX, afterX)
	}

	out.Send(proto.Leave{Frac: 0.25})
	time.Sleep(200 * time.Millisecond)
	x, y, _ := h.cursor()
	if x > start.X+10 || y < start.Y+ht*0.2 || y > start.Y+ht*0.3 {
		t.Fatalf("after Leave cursor at %.0f,%.0f; want left edge at 25%% height of %s", x, y, start.Name)
	}
	move(mouse, 30)
	time.Sleep(100 * time.Millisecond)
	x2, _, _ := h.cursor()
	if x2 <= x {
		t.Fatalf("local cursor did not move after Leave (%v -> %v): devices still grabbed", x, x2)
	}
	t.Logf("returned to %.0f,%.0f and local input works again", x, y)
}
