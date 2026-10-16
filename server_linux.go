package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/LakshitXD/desksynk/internal/proto"
)

// peer is the one connected Mac.
type peer struct {
	conn *tls.Conn
	out  *proto.Sender
}

type peerMsg struct {
	p   *peer
	msg proto.Msg
	err error
}

type server struct {
	side    proto.Edge // where the Mac sits relative to the Linux desktop
	hypr    *hyprland
	clip    clipboardTool
	devices map[string]*device
	events  chan devEvent

	peer   *peer
	remote bool // input is grabbed and forwarded to the Mac

	only     string
	pressed  map[uint16]bool // keys and buttons currently held
	chordArm bool
	lastClip string
}

var linuxModifiers = map[uint16]string{
	29: "ctrl", 97: "ctrl",
	56: "alt", 100: "alt",
	42: "shift", 54: "shift",
}

var linuxButtons = map[uint16]proto.Button{
	0x110: proto.BtnLeft, 0x111: proto.BtnRight, 0x112: proto.BtnMiddle,
	0x113: proto.BtnBack, 0x114: proto.BtnForward, 0x115: proto.BtnForward, 0x116: proto.BtnBack,
}

func runServer(o serverOpts) error {
	side, err := proto.ParseEdge(o.side)
	if err != nil {
		return err
	}
	sock, err := findHyprSocket()
	if err != nil {
		return err
	}
	cfg, fp, err := tlsConfig("server")
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp4", o.listen)
	if err != nil {
		return err
	}
	logf("listening on %s, Mac is on the %s; fingerprint %s", ln.Addr(), side, short(fp))
	logf("press and release Ctrl+Alt+Shift to switch screens by hand")

	s := &server{
		side:    side,
		hypr:    &hyprland{socket: sock},
		devices: map[string]*device{},
		events:  make(chan devEvent, 1024),
		pressed: map[uint16]bool{},
		only:    o.onlyDevice,
	}
	if !o.noClipboard && os.Getenv("WAYLAND_DISPLAY") != "" {
		s.clip = clipboardTool{
			get: []string{"wl-paste", "--no-newline", "--type", "text"},
			set: []string{"wl-copy"},
		}
	}
	s.scanDevices()
	if len(s.devices) == 0 {
		return fmt.Errorf("no keyboards or mice readable in /dev/input; add yourself to the input group")
	}

	peers := make(chan *peer)
	msgs := make(chan peerMsg, 256)
	go acceptLoop(ln, cfg, peers, msgs)

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	tick := time.NewTicker(pingEvery)
	defer tick.Stop()

	for {
		select {
		case e := <-s.events:
			s.handleDevice(e)
		case p := <-peers:
			if s.peer != nil {
				s.dropPeer("replaced by a new connection")
			}
			s.peer = p
			logf("Mac connected from %s", p.conn.RemoteAddr())
		case m := <-msgs:
			if m.p != s.peer {
				continue
			}
			if m.err != nil {
				s.dropPeer(m.err.Error())
				continue
			}
			s.handlePeer(m.msg)
		case <-tick.C:
			s.scanDevices()
			if s.peer != nil {
				if err := s.peer.out.Err(); err != nil {
					s.dropPeer(err.Error())
				} else {
					s.peer.out.Send(proto.Ping{})
				}
			}
		case <-sigs:
			s.setRemote(false)
			return nil
		}
	}
}

func acceptLoop(ln net.Listener, cfg *tls.Config, peers chan<- *peer, msgs chan<- peerMsg) {
	for {
		c, err := ln.Accept()
		if err != nil {
			logf("accept: %v", err)
			time.Sleep(time.Second)
			continue
		}
		go func() {
			tuneConn(c)
			tc := tls.Server(c, cfg)
			tc.SetDeadline(time.Now().Add(10 * time.Second))
			r := bufio.NewReader(tc)
			m, err := proto.Read(r)
			if err != nil {
				logf("rejected %s: %v", c.RemoteAddr(), err)
				tc.Close()
				return
			}
			if h, ok := m.(proto.Hello); !ok || h.Version != proto.Version {
				logf("rejected %s: version mismatch (%#v), update both sides", c.RemoteAddr(), m)
				tc.Close()
				return
			}
			tc.SetDeadline(time.Time{})
			p := &peer{conn: tc, out: proto.NewSender(tc)}
			peers <- p
			for {
				tc.SetReadDeadline(time.Now().Add(peerTimeout))
				m, err := proto.Read(r)
				msgs <- peerMsg{p, m, err}
				if err != nil {
					return
				}
			}
		}()
	}
}

func (s *server) dropPeer(why string) {
	logf("Mac disconnected: %s", why)
	s.peer.out.Close()
	s.peer.conn.Close()
	s.peer = nil
	s.setRemote(false)
}

func (s *server) scanDevices() {
	for _, path := range inputNodes() {
		if _, ok := s.devices[path]; ok {
			continue
		}
		d, err := openDevice(path)
		if err != nil {
			continue
		}
		if s.only != "" && d.name != s.only {
			d.f.Close()
			continue
		}
		s.devices[path] = d
		if s.remote {
			d.grab(true)
		}
		logf("using %s (%s)", d.name, path)
		go d.read(s.events)
	}
}

// setRemote is the only place input grabbing changes, so grab state can never
// disagree with s.remote.
func (s *server) setRemote(on bool) {
	if s.remote == on {
		return
	}
	s.remote = on
	for _, d := range s.devices {
		if err := d.grab(on); err != nil {
			logf("grab %s: %v", d.name, err)
		}
	}
}

func (s *server) enter(frac float64) {
	if s.peer == nil {
		return
	}
	s.setRemote(true)
	if text, ok := s.clip.read(); ok && text != s.lastClip {
		s.lastClip = text
		s.peer.out.Send(proto.Clipboard{Text: text})
	}
	s.peer.out.Send(proto.Enter{Edge: s.side.Opposite(), Frac: frac})
}

func (s *server) handlePeer(m proto.Msg) {
	switch m := m.(type) {
	case proto.Leave:
		if s.remote {
			s.setRemote(false)
			s.hypr.warpToEdge(s.side, m.Frac)
		}
	case proto.Clipboard:
		if m.Text != s.lastClip {
			s.lastClip = m.Text
			s.clip.write(m.Text)
		}
	case proto.Ping:
		s.peer.out.Send(proto.Pong{})
	}
}

func (s *server) handleDevice(e devEvent) {
	d := e.dev
	if e.err != nil {
		logf("lost %s: %v", d.name, e.err)
		d.f.Close()
		delete(s.devices, d.path)
		clear(s.pressed) // its held keys will never report a release
		return
	}
	ev := e.ev
	switch ev.typ {
	case evSyn:
		if d.dx != 0 || d.dy != 0 {
			s.motion(d.dx, d.dy)
			d.dx, d.dy = 0, 0
		}
	case evRel:
		s.rel(d, ev)
	case evKey:
		s.key(ev)
	}
}

func (s *server) motion(dx, dy int32) {
	if s.remote {
		s.peer.out.Motion(dx, dy)
		return
	}
	if s.peer == nil || len(s.pressed) > 0 {
		return // never switch mid-drag or with keys held: their releases would go missing
	}
	toward := map[proto.Edge]bool{proto.Left: dx < 0, proto.Right: dx > 0, proto.Top: dy < 0, proto.Bottom: dy > 0}
	if !toward[s.side] {
		return
	}
	if frac, ok := s.hypr.crossing(s.side); ok {
		s.enter(frac)
	}
}

func (s *server) rel(d *device, ev inputEvent) {
	switch ev.code {
	case relX:
		d.dx += ev.value
		return
	case relY:
		d.dy += ev.value
		return
	}
	if !s.remote {
		return
	}
	var sc proto.Scroll
	switch {
	case ev.code == relWheelHiRes:
		sc.DY = ev.value
	case ev.code == relHWheelHiRes:
		sc.DX = ev.value
	case ev.code == relWheel && !d.hiRes:
		sc.DY = ev.value * 120
	case ev.code == relHWheel && !d.hiRes:
		sc.DX = ev.value * 120
	default:
		return
	}
	s.peer.out.Send(sc)
}

func (s *server) key(ev inputEvent) {
	code := ev.code
	btn, isButton := linuxButtons[code]
	if code >= btnLeft && !isButton {
		return // touch, stylus and joystick codes
	}
	if code >= 0x100 && code < btnLeft {
		return
	}
	if ev.value == 0 {
		delete(s.pressed, code)
	} else {
		s.pressed[code] = true
	}
	toggle := s.trackChord(code, ev.value)

	if s.remote {
		if isButton {
			s.peer.out.Send(proto.ButtonEv{Button: btn, Down: ev.value != 0})
		} else {
			s.peer.out.Send(proto.Key{Code: code, Value: proto.KeyValue(ev.value)})
		}
	}
	if !toggle {
		return
	}
	if s.remote {
		s.peer.out.Send(proto.Leave{Frac: 0.5})
		s.setRemote(false)
	} else if s.peer != nil {
		s.enter(0.5)
	}
}

// trackChord recognises a press and release of Ctrl+Alt+Shift with nothing
// else. It fires on the final release so no key is held across the switch.
func (s *server) trackChord(code uint16, value int32) bool {
	if value == 2 {
		return false
	}
	if value == 1 {
		if _, mod := linuxModifiers[code]; !mod {
			s.chordArm = false
			return false
		}
		held := map[string]int{}
		for c := range s.pressed {
			name, mod := linuxModifiers[c]
			if !mod {
				return false
			}
			held[name]++
		}
		if len(held) == 3 && len(s.pressed) == 3 {
			s.chordArm = true
		}
		return false
	}
	if len(s.pressed) == 0 && s.chordArm {
		s.chordArm = false
		return true
	}
	return false
}
