package proto

import (
	"bufio"
	"bytes"
	"io"
	"sync"
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
	s := NewSender(w, false)

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

func TestWindowedSenderHoldsMotionUntilAcked(t *testing.T) {
	var buf safeBuffer
	s := NewSender(&buf, true)
	for range MotionWindow {
		s.Motion(1, 0)
		time.Sleep(10 * time.Millisecond)
	}
	for range 300 {
		s.Motion(1, 0)
	}
	time.Sleep(30 * time.Millisecond)
	if got := buf.messages(t); len(got) != MotionWindow {
		t.Fatalf("sent %d motions with no acks, want the window of %d", len(got), MotionWindow)
	}
	s.Send(Key{30, KeyDown}) // non-motion traffic is never held back
	time.Sleep(10 * time.Millisecond)
	s.Acked()
	time.Sleep(10 * time.Millisecond)
	s.Close()
	got := buf.messages(t)
	want := []Msg{Motion{1, 0}, Motion{1, 0}, Motion{300, 0}, Key{30, KeyDown}}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message %d: got %#v want %#v", i, got[i], want[i])
		}
	}
	if r := s.TakeRTTs(); len(r) != 1 {
		t.Fatalf("want 1 round trip measured, got %d", len(r))
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) messages(t *testing.T) []Msg {
	s.mu.Lock()
	r := bufio.NewReader(bytes.NewReader(s.b.Bytes()))
	s.mu.Unlock()
	var out []Msg
	for {
		m, err := Read(r)
		if err != nil {
			return out
		}
		out = append(out, m)
	}
}
