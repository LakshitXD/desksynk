package proto

import (
	"bufio"
	"bytes"
	"io"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	msgs := []Msg{
		Hello{Version},
		Enter{Bottom, 0.5},
		Leave{1},
		Motion{-70000, 12},
		ButtonEv{BtnForward, true},
		Key{125, KeyRepeat},
		Scroll{-120, 360},
		Clipboard{"héllo\nworld"},
		Clipboard{""},
		Ping{},
		Pong{},
	}
	var buf bytes.Buffer
	for _, m := range msgs {
		if err := Write(&buf, m); err != nil {
			t.Fatal(err)
		}
	}
	r := bufio.NewReader(&buf)
	for _, want := range msgs {
		got, err := Read(r)
		if err != nil {
			t.Fatal(err)
		}
		if e, ok := want.(Enter); ok {
			g := got.(Enter)
			if g.Edge != e.Edge || abs(g.Frac-e.Frac) > 1e-4 {
				t.Fatalf("got %+v want %+v", g, e)
			}
			continue
		}
		if got != want {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
	if _, err := Read(r); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestRejectsOversizedPayload(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader([]byte{8, 0xff, 0xff, 0xff, 0xff}))
	if _, err := Read(r); err == nil {
		t.Fatal("oversized payload accepted")
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// stallWriter blocks every write until released, like a Wi-Fi link that is
// off-channel for AWDL.
type stallWriter struct {
	release chan struct{}
	out     bytes.Buffer
}

func (s *stallWriter) Write(p []byte) (int, error) {
	<-s.release
	return s.out.Write(p)
}

func TestSenderCoalescesMotionDuringStall(t *testing.T) {
	w := &stallWriter{release: make(chan struct{})}
	s := NewSender(w)

	s.Motion(1, 1)
	time.Sleep(20 * time.Millisecond) // writer is now blocked on the first motion
	for range 1000 {
		s.Motion(2, -1)
	}
	s.Send(Key{30, KeyDown})
	for range 500 {
		s.Motion(1, 0)
	}
	close(w.release)
	time.Sleep(50 * time.Millisecond)
	s.Close()

	r := bufio.NewReader(&w.out)
	var got []Msg
	for {
		m, err := Read(r)
		if err != nil {
			break
		}
		got = append(got, m)
	}
	want := []Msg{Motion{1, 1}, Motion{2000, -1000}, Key{30, KeyDown}, Motion{500, 0}}
	if len(got) != len(want) {
		t.Fatalf("got %d messages %v, want %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message %d: got %#v want %#v", i, got[i], want[i])
		}
	}
}
