package main

import (
	"bufio"
	"crypto/tls"
	"math"
	"net"
	"time"

	"github.com/LakshitXD/desksynk/internal/proto"
)

func runClient(o clientOpts) error {
	if err := loadCoreGraphics(); err != nil {
		return err
	}
	preventAppNap()
	if !axIsProcessTrusted() {
		logf("this process lacks Accessibility permission, so macOS will drop the injected input.")
		logf("open System Settings > Privacy & Security > Accessibility, add the app running desksynk (e.g. Terminal), then restart desksynk")
	}
	cfg, fp, err := tlsConfig("client")
	if err != nil {
		return err
	}
	logf("client fingerprint %s", short(fp))
	addr := o.addr
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "24810")
	}
	src := cgEventSourceCreate(cgEventSourceStateHID)
	// Without this, macOS ignores the local trackpad for 250 ms after every
	// injected event.
	cgEventSourceSetSuppression(src, 0)
	m := &mac{opts: o, src: src, held: map[uint16]bool{}}
	if !o.noClipboard {
		m.clip = clipboardTool{get: []string{"pbpaste"}, set: []string{"pbcopy"}}
	}
	backoff := time.Second
	for {
		start := time.Now()
		err := m.session(addr, cfg)
		m.deactivate()
		logf("disconnected: %v", err)
		if time.Since(start) > 10*time.Second {
			backoff = time.Second
		}
		time.Sleep(backoff)
		backoff = min(backoff*2, 5*time.Second)
	}
}

// mac injects the forwarded input on this Mac.
type mac struct {
	opts clientOpts
	src  uintptr
	clip clipboardTool
	out  *proto.Sender

	active   bool
	edge     proto.Edge // the edge facing Linux
	pos      cgPoint
	lastPost time.Time
	screens  []cgRect

	held       map[uint16]bool // mac keycodes down
	capsLock   bool
	buttons    [5]bool
	scrollAcc  [2]int32
	lastClick  time.Time
	lastBtn    proto.Button
	clickPos   cgPoint
	clickCount int64
	lastClip   string
}

func (m *mac) session(addr string, cfg *tls.Config) error {
	// tcp4 on purpose: a .local name can resolve to an IPv6 link-local
	// address on the AWDL (AirDrop) interface, which comes and goes.
	raw, err := net.DialTimeout("tcp4", addr, 5*time.Second)
	if err != nil {
		return err
	}
	tuneConn(raw)
	c := tls.Client(raw, cfg)
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if err := c.Handshake(); err != nil {
		return err
	}
	c.SetDeadline(time.Time{})
	m.out = proto.NewSender(c, false)
	defer m.out.Close()
	m.out.Send(proto.Hello{Version: proto.Version})
	logf("connected to %s", addr)

	r := bufio.NewReader(c)
	for {
		c.SetReadDeadline(time.Now().Add(peerTimeout))
		msg, err := proto.Read(r)
		if err != nil {
			return err
		}
		if err := m.out.Err(); err != nil {
			return err
		}
		start := time.Now()
		m.handle(msg)
		if d := time.Since(start); d > 30*time.Millisecond {
			logf("slow: applying %T took %v", msg, d.Round(time.Millisecond))
		}
	}
}

func (m *mac) handle(msg proto.Msg) {
	switch msg := msg.(type) {
	case proto.Ping:
		m.out.Send(proto.Pong{})
	case proto.Clipboard:
		if msg.Text != m.lastClip {
			m.lastClip = msg.Text
			m.clip.write(msg.Text)
		}
	case proto.Enter:
		m.enter(msg.Edge, msg.Frac)
	case proto.Leave:
		m.deactivate()
	case proto.Motion:
		if m.active {
			m.motion(float64(msg.DX)*m.opts.speed, float64(msg.DY)*m.opts.speed)
		}
		m.out.Send(proto.MotionAck{})
	case proto.ButtonEv:
		if m.active {
			m.button(msg.Button, msg.Down)
		}
	case proto.Key:
		if m.active {
			m.key(msg.Code, msg.Value)
		}
	case proto.Scroll:
		if m.active {
			m.scroll(msg.DX, msg.DY)
		}
	}
}

func (m *mac) enter(edge proto.Edge, frac float64) {
	m.screens = displays()
	if len(m.screens) == 0 {
		return
	}
	outer := func(r cgRect) float64 {
		switch edge {
		case proto.Left:
			return -r.Origin.X
		case proto.Right:
			return r.Origin.X + r.Size.W
		case proto.Top:
			return -r.Origin.Y
		default:
			return r.Origin.Y + r.Size.H
		}
	}
	d := m.screens[0]
	for _, r := range m.screens[1:] {
		if outer(r) > outer(d) {
			d = r
		}
	}
	o, s := d.Origin, d.Size
	switch edge {
	case proto.Left:
		m.pos = cgPoint{o.X, o.Y + frac*s.H}
	case proto.Right:
		m.pos = cgPoint{o.X + s.W - 1, o.Y + frac*s.H}
	case proto.Top:
		m.pos = cgPoint{o.X + frac*s.W, o.Y}
	case proto.Bottom:
		m.pos = cgPoint{o.X + frac*s.W, o.Y + s.H - 1}
	}
	m.edge, m.active = edge, true
	m.postMove(0, 0)
}

// deactivate releases everything held so no key or button stays stuck when
// control goes back to Linux or the connection drops.
func (m *mac) deactivate() {
	if !m.active {
		return
	}
	for k := range m.held {
		m.postKey(k, false, false)
	}
	for b, down := range m.buttons {
		if down {
			m.button(proto.Button(b), false)
		}
	}
	m.active = false
}

func (m *mac) screenAt(x, y float64) (cgRect, bool) {
	for _, r := range m.screens {
		if r.contains(x, y) {
			return r, true
		}
	}
	return cgRect{}, false
}

func (m *mac) beyondEdge(x, y float64) bool {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, r := range m.screens {
		minX, minY = min(minX, r.Origin.X), min(minY, r.Origin.Y)
		maxX, maxY = max(maxX, r.Origin.X+r.Size.W), max(maxY, r.Origin.Y+r.Size.H)
	}
	switch m.edge {
	case proto.Left:
		return x < minX
	case proto.Right:
		return x >= maxX
	case proto.Top:
		return y < minY
	default:
		return y >= maxY
	}
}

func (m *mac) anyButton() bool {
	for _, b := range m.buttons {
		if b {
			return true
		}
	}
	return false
}

func (m *mac) motion(dx, dy float64) {
	if time.Since(m.lastPost) > 200*time.Millisecond {
		// Someone may have used the Mac's own trackpad meanwhile.
		m.pos = cursorLocation()
		m.screens = displays()
	}
	nx, ny := m.pos.X+dx, m.pos.Y+dy
	switch {
	case m.onScreen(nx, ny):
		m.pos = cgPoint{nx, ny}
	case m.beyondEdge(nx, ny) && !m.anyButton():
		m.leave()
		return
	case m.onScreen(nx, m.pos.Y):
		m.pos.X = nx
	case m.onScreen(m.pos.X, ny):
		m.pos.Y = ny
	default:
		if r, ok := m.screenAt(m.pos.X, m.pos.Y); ok {
			m.pos.X = min(max(nx, r.Origin.X), r.Origin.X+r.Size.W-1)
			m.pos.Y = min(max(ny, r.Origin.Y), r.Origin.Y+r.Size.H-1)
		}
	}
	m.postMove(dx, dy)
}

func (m *mac) onScreen(x, y float64) bool {
	_, ok := m.screenAt(x, y)
	return ok
}

func (m *mac) leave() {
	frac := 0.5
	if r, ok := m.screenAt(m.pos.X, m.pos.Y); ok {
		if m.edge.Horizontal() {
			frac = (m.pos.Y - r.Origin.Y) / r.Size.H
		} else {
			frac = (m.pos.X - r.Origin.X) / r.Size.W
		}
	}
	if text, ok := m.clip.read(); ok && text != m.lastClip {
		m.lastClip = text
		m.out.Send(proto.Clipboard{Text: text})
	}
	m.deactivate()
	m.out.Send(proto.Leave{Frac: frac})
}

func (m *mac) flags() uint64 {
	var f uint64
	for k := range m.held {
		f |= modifierFlag[k]
	}
	if m.capsLock {
		f |= flagAlphaShift
	}
	return f
}

func (m *mac) post(ev uintptr) {
	cgEventPost(cgHIDEventTap, ev)
	cfRelease(ev)
	m.lastPost = time.Now()
}

func (m *mac) postMove(dx, dy float64) {
	typ, btn := uint32(cgMouseMoved), uint32(0)
	switch {
	case m.buttons[proto.BtnLeft]:
		typ = cgLeftMouseDragged
	case m.buttons[proto.BtnRight]:
		typ, btn = cgRightMouseDragged, 1
	case m.anyButton():
		typ, btn = cgOtherMouseDragged, 2
	}
	ev := cgEventCreateMouseEvent(m.src, typ, m.pos, btn)
	cgEventSetIntegerValueField(ev, cgFieldDeltaX, int64(math.Round(dx)))
	cgEventSetIntegerValueField(ev, cgFieldDeltaY, int64(math.Round(dy)))
	cgEventSetFlags(ev, m.flags())
	m.post(ev)
}

func (m *mac) button(b proto.Button, down bool) {
	if int(b) >= len(m.buttons) || m.buttons[b] == down {
		return
	}
	m.buttons[b] = down
	var typ uint32
	switch {
	case b == proto.BtnLeft && down:
		typ = cgLeftMouseDown
	case b == proto.BtnLeft:
		typ = cgLeftMouseUp
	case b == proto.BtnRight && down:
		typ = cgRightMouseDown
	case b == proto.BtnRight:
		typ = cgRightMouseUp
	case down:
		typ = cgOtherMouseDown
	default:
		typ = cgOtherMouseUp
	}
	if down {
		near := math.Abs(m.pos.X-m.clickPos.X) < 5 && math.Abs(m.pos.Y-m.clickPos.Y) < 5
		if b == m.lastBtn && near && time.Since(m.lastClick) < 500*time.Millisecond {
			m.clickCount++
		} else {
			m.clickCount = 1
		}
		m.lastBtn, m.lastClick, m.clickPos = b, time.Now(), m.pos
	}
	ev := cgEventCreateMouseEvent(m.src, typ, m.pos, uint32(b))
	cgEventSetIntegerValueField(ev, cgFieldButtonNumber, int64(b))
	cgEventSetIntegerValueField(ev, cgFieldClickState, m.clickCount)
	cgEventSetFlags(ev, m.flags())
	m.post(ev)
}

var altCmdSwap = map[uint16]uint16{56: 125, 125: 56, 100: 126, 126: 100}

func (m *mac) key(code uint16, value proto.KeyValue) {
	if m.opts.altIsCmd {
		if c, ok := altCmdSwap[code]; ok {
			code = c
		}
	}
	k, ok := evdevToMac[code]
	if !ok {
		return
	}
	m.postKey(k, value != proto.KeyUp, value == proto.KeyRepeat)
}

const kvkCapsLock = 0x39

func (m *mac) postKey(k uint16, down, repeat bool) {
	_, isMod := modifierFlag[k]
	if down {
		m.held[k] = true
	} else {
		delete(m.held, k)
	}
	if k == kvkCapsLock {
		if !down || repeat {
			return
		}
		m.capsLock = !m.capsLock
		isMod = true
	}
	ev := cgEventCreateKeyboardEvent(m.src, k, down)
	if isMod {
		cgEventSetType(ev, cgFlagsChanged)
		cgEventSetFlags(ev, m.flags())
	} else {
		cgEventSetFlags(ev, m.flags()|keyFlags(k))
	}
	if repeat {
		cgEventSetIntegerValueField(ev, cgFieldKeyboardAutorept, 1)
	}
	m.post(ev)
}

func (m *mac) scroll(dx, dy int32) {
	m.scrollAcc[0] += dx
	m.scrollAcc[1] += dy
	lx, ly := m.scrollAcc[0]/120, m.scrollAcc[1]/120
	if lx == 0 && ly == 0 {
		return
	}
	m.scrollAcc[0] -= lx * 120
	m.scrollAcc[1] -= ly * 120
	if m.opts.naturalScroll {
		lx, ly = -lx, -ly
	}
	// CoreGraphics: wheel1 positive scrolls up, wheel2 positive scrolls left.
	ev := cgEventCreateScrollWheel2(m.src, cgScrollUnitLine, 2, ly, -lx, 0)
	cgEventSetFlags(ev, m.flags())
	m.post(ev)
}
