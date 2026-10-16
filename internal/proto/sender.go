package proto

import (
	"bufio"
	"io"
	"sync"
)

// Sender writes messages from a single goroutine and coalesces pointer motion.
//
// When the link stalls (AWDL channel hopping while AirDrop is on, Wi-Fi power
// save), motion that piles up is summed into one Motion instead of being
// queued. The pointer jumps to where it should be once the link recovers
// instead of replaying a backlog in slow motion. Every other message keeps its
// order relative to motion.
type Sender struct {
	mu     sync.Mutex
	wake   *sync.Cond
	queue  []Msg
	dx, dy int32
	err    error
	closed bool
}

func NewSender(w io.Writer) *Sender {
	s := &Sender{}
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

func (s *Sender) flushMotionLocked() {
	if s.dx != 0 || s.dy != 0 {
		s.queue = append(s.queue, Motion{s.dx, s.dy})
		s.dx, s.dy = 0, 0
	}
}

func (s *Sender) loop(w *bufio.Writer) {
	for {
		s.mu.Lock()
		for !s.closed && len(s.queue) == 0 && s.dx == 0 && s.dy == 0 {
			s.wake.Wait()
		}
		if s.closed {
			s.mu.Unlock()
			return
		}
		s.flushMotionLocked()
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
