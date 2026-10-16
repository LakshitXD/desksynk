package main

import "testing"

func TestChordFiresOnFinalRelease(t *testing.T) {
	type step struct {
		code  uint16
		value int32
	}
	const ctrl, alt, shift, rshift, a = 29, 56, 42, 54, 30
	cases := []struct {
		name  string
		steps []step
		want  bool
	}{
		{"ctrl alt shift", []step{{ctrl, 1}, {alt, 1}, {shift, 1}, {alt, 0}, {ctrl, 0}, {shift, 0}}, true},
		{"right shift counts", []step{{shift, 1}, {ctrl, 1}, {alt, 1}, {shift, 0}, {ctrl, 0}, {alt, 0}}, true},
		{"extra key cancels", []step{{ctrl, 1}, {alt, 1}, {shift, 1}, {a, 1}, {a, 0}, {ctrl, 0}, {alt, 0}, {shift, 0}}, false},
		{"two shifts are not a chord", []step{{ctrl, 1}, {shift, 1}, {rshift, 1}, {ctrl, 0}, {shift, 0}, {rshift, 0}}, false},
		{"ctrl alt only", []step{{ctrl, 1}, {alt, 1}, {ctrl, 0}, {alt, 0}}, false},
	}
	for _, c := range cases {
		s := &server{pressed: map[uint16]bool{}}
		fired := false
		for _, st := range c.steps {
			if st.value == 0 {
				delete(s.pressed, st.code)
			} else {
				s.pressed[st.code] = true
			}
			fired = s.trackChord(st.code, st.value) || fired
		}
		if fired != c.want {
			t.Errorf("%s: fired=%v want %v", c.name, fired, c.want)
		}
	}
}
