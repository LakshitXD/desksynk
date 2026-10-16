// Package proto is the desksynk wire format: a 1-byte type, a 4-byte
// big-endian payload length, then the payload.
package proto

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

const Version = 2

const maxPayload = 4 << 20

// Edge is a side of a screen. In Enter/Leave it names the client edge the
// cursor crosses.
type Edge uint8

const (
	Left Edge = iota
	Right
	Top
	Bottom
)

var edgeNames = [...]string{"left", "right", "top", "bottom"}

func (e Edge) String() string {
	if int(e) < len(edgeNames) {
		return edgeNames[e]
	}
	return fmt.Sprintf("edge(%d)", e)
}

func (e Edge) Opposite() Edge { return e ^ 1 }

func (e Edge) Horizontal() bool { return e == Left || e == Right }

func ParseEdge(s string) (Edge, error) {
	for i, n := range edgeNames {
		if strings.EqualFold(s, n) {
			return Edge(i), nil
		}
	}
	return 0, fmt.Errorf("unknown edge %q (want left, right, top or bottom)", s)
}

// Button ids are platform neutral.
type Button uint8

const (
	BtnLeft Button = iota
	BtnRight
	BtnMiddle
	BtnBack
	BtnForward
)

// KeyValue mirrors evdev: 0 release, 1 press, 2 autorepeat.
type KeyValue uint8

const (
	KeyUp KeyValue = iota
	KeyDown
	KeyRepeat
)

type Msg interface{ msgType() byte }

type (
	Hello struct{ Version uint16 }
	// Enter hands the cursor to the client at Edge, Frac (0..1) along it.
	Enter struct {
		Edge Edge
		Frac float64
	}
	// Leave hands the cursor back. Frac is the position along the shared edge.
	Leave    struct{ Frac float64 }
	Motion   struct{ DX, DY int32 }
	ButtonEv struct {
		Button Button
		Down   bool
	}
	// Key carries a Linux evdev keycode.
	Key struct {
		Code  uint16
		Value KeyValue
	}
	// Scroll is in 1/120ths of a wheel notch; positive DY is up, positive DX is right.
	Scroll    struct{ DX, DY int32 }
	Clipboard struct{ Text string }
	Ping      struct{}
	Pong      struct{}
	// MotionAck tells the server one Motion has been applied.
	MotionAck struct{}
)

func (Hello) msgType() byte     { return 1 }
func (Enter) msgType() byte     { return 2 }
func (Leave) msgType() byte     { return 3 }
func (Motion) msgType() byte    { return 4 }
func (ButtonEv) msgType() byte  { return 5 }
func (Key) msgType() byte       { return 6 }
func (Scroll) msgType() byte    { return 7 }
func (Clipboard) msgType() byte { return 8 }
func (Ping) msgType() byte      { return 9 }
func (Pong) msgType() byte      { return 10 }
func (MotionAck) msgType() byte { return 11 }

func fracToU16(f float64) uint16 {
	f = min(max(f, 0), 1)
	return uint16(f*65535 + 0.5)
}

func u16ToFrac(v uint16) float64 { return float64(v) / 65535 }

func Write(w io.Writer, m Msg) error {
	var p []byte
	be := binary.BigEndian
	switch m := m.(type) {
	case Hello:
		p = be.AppendUint16(p, m.Version)
	case Enter:
		p = append(p, byte(m.Edge))
		p = be.AppendUint16(p, fracToU16(m.Frac))
	case Leave:
		p = be.AppendUint16(p, fracToU16(m.Frac))
	case Motion:
		p = be.AppendUint32(p, uint32(m.DX))
		p = be.AppendUint32(p, uint32(m.DY))
	case ButtonEv:
		p = append(p, byte(m.Button), boolByte(m.Down))
	case Key:
		p = be.AppendUint16(p, m.Code)
		p = append(p, byte(m.Value))
	case Scroll:
		p = be.AppendUint32(p, uint32(m.DX))
		p = be.AppendUint32(p, uint32(m.DY))
	case Clipboard:
		if len(m.Text) > maxPayload {
			return errors.New("clipboard too large")
		}
		p = []byte(m.Text)
	case Ping, Pong, MotionAck:
	default:
		return fmt.Errorf("unknown message %T", m)
	}
	hdr := [5]byte{m.msgType()}
	be.PutUint32(hdr[1:], uint32(len(p)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(p)
	return err
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

func Read(r *bufio.Reader) (Msg, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxPayload {
		return nil, fmt.Errorf("payload too large: %d", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return nil, err
	}
	be := binary.BigEndian
	need := func(k int) error {
		if len(p) < k {
			return fmt.Errorf("message type %d: short payload %d < %d", hdr[0], len(p), k)
		}
		return nil
	}
	switch hdr[0] {
	case 1:
		if err := need(2); err != nil {
			return nil, err
		}
		return Hello{be.Uint16(p)}, nil
	case 2:
		if err := need(3); err != nil {
			return nil, err
		}
		if p[0] > byte(Bottom) {
			return nil, fmt.Errorf("bad edge %d", p[0])
		}
		return Enter{Edge(p[0]), u16ToFrac(be.Uint16(p[1:]))}, nil
	case 3:
		if err := need(2); err != nil {
			return nil, err
		}
		return Leave{u16ToFrac(be.Uint16(p))}, nil
	case 4:
		if err := need(8); err != nil {
			return nil, err
		}
		return Motion{int32(be.Uint32(p)), int32(be.Uint32(p[4:]))}, nil
	case 5:
		if err := need(2); err != nil {
			return nil, err
		}
		return ButtonEv{Button(p[0]), p[1] != 0}, nil
	case 6:
		if err := need(3); err != nil {
			return nil, err
		}
		return Key{be.Uint16(p), KeyValue(p[2])}, nil
	case 7:
		if err := need(8); err != nil {
			return nil, err
		}
		return Scroll{int32(be.Uint32(p)), int32(be.Uint32(p[4:]))}, nil
	case 8:
		return Clipboard{string(p)}, nil
	case 9:
		return Ping{}, nil
	case 10:
		return Pong{}, nil
	case 11:
		return MotionAck{}, nil
	}
	return nil, fmt.Errorf("unknown message type %d", hdr[0])
}
