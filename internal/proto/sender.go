package proto

import (
	"bufio"
	"io"
	"slices"
	"sync"
	"time"
)

// MotionWindow is how many Motion messages may be unacknowledged at once.
//
// Kernel socket buffers and the Wi-Fi access point both queue packets
// without limit, so a sender that only notices blocked writes never sees a
// stall: the backlog piles up in the network and the pointer trails your
// hand. Capping in-flight motion keeps that queue empty. Motion that arrives
// meanwhile is summed into the next message, so a slow or stalled link (AWDL
// channel hopping while AirDrop is on) costs one jump instead of a lag.
const MotionWindow = 2

// Sender writes messages from a single goroutine. Every message except
// Motion is sent in order as soon as possible. Motion is coalesced and,
// when windowed, paced by MotionAck.
type Sender struct {
	mu       sync.Mutex
	wake     *sync.Cond
	queue    []Msg
	dx, dy   int32
	windowed bool
	inFlight []time.Time // send times of unacknowledged Motion
	rtts     []time.Duration
	err      error
	closed   bool
}

func NewSender(w io.Writer, windowed bool) *Sender {
	s := &Sender{windowed: windowed}
	s.wake = sync.NewCond(&s.mu)
	go s.loop(bufio.NewWriter(w))
	return s
}

func (s *Sender) Motion(dx, dy int32) {
	s.mu.Lock()
	s.dx += dx
	s.dy += dy
	s.mu.Unlock()
	s.wake.Signal()
}

func (s *Sender) Send(m Msg) {
	s.mu.Lock()
	s.flushMotionLocked()
	s.queue = append(s.queue, m)
	s.mu.Unlock()
	s.wake.Signal()
}

// Acked records that the peer applied the oldest outstanding Motion.
func (s *Sender) Acked() {
	s.mu.Lock()
	if len(s.inFlight) > 0 {
		s.rtts = append(s.rtts, time.Since(s.inFlight[0]))
		s.inFlight = s.inFlight[1:]
	}
	s.mu.Unlock()
	s.wake.Signal()
}

// TakeRTTs returns the motion round trips measured since the last call,
// sorted ascending.
func (s *Sender) TakeRTTs() []time.Duration {
	s.mu.Lock()
	r := s.rtts
	s.rtts = nil
	s.mu.Unlock()
	slices.Sort(r)
	return r
}

// Err reports the first write error, or nil while the connection is healthy.
func (s *Sender) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *Sender) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.wake.Signal()
}

func (s *Sender) motionReadyLocked() bool {
	return (s.dx != 0 || s.dy != 0) && (!s.windowed || len(s.inFlight) < MotionWindow)
}

func (s *Sender) flushMotionLocked() {
	if s.dx != 0 || s.dy != 0 {
		s.queue = append(s.queue, Motion{s.dx, s.dy})
		s.dx, s.dy = 0, 0
		if s.windowed {
			s.inFlight = append(s.inFlight, time.Now())
		}
	}
}

func (s *Sender) loop(w *bufio.Writer) {
	for {
		s.mu.Lock()
		for !s.closed && len(s.queue) == 0 && !s.motionReadyLocked() {
			s.wake.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		if s.motionReadyLocked() {
			s.flushMotionLocked()
		}
		batch := s.queue
		s.queue = nil
		s.mu.Unlock()

		var err error
		for _, m := range batch {
			if err = Write(w, m); err != nil {
				break
			}
		}
		if err == nil {
			err = w.Flush()
		}
		if err != nil {
			s.mu.Lock()
			s.err = err
			s.closed = true
			s.mu.Unlock()
			return
		}
	}
}
