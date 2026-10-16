package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	pingEvery   = 2 * time.Second
	peerTimeout = 15 * time.Second
	maxClip     = 1 << 20
)

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, time.Now().Format("15:04:05 ")+format+"\n", args...)
}

// tuneConn turns off Nagle and marks packets as interactive (DSCP EF), which
// Wi-Fi WMM maps to the voice queue so input packets jump ahead of bulk
// traffic, including AirDrop transfers.
func tuneConn(c net.Conn) {
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return
	}
	tc.SetNoDelay(true)
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(10 * time.Second)
	if raw, err := tc.SyscallConn(); err == nil {
		raw.Control(func(fd uintptr) {
			syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TOS, 0xb8)
		})
	}
}

type clipboardTool struct{ get, set []string }

func (t clipboardTool) read() (string, bool) {
	if len(t.get) == 0 {
		return "", false
	}
	cmd := exec.Command(t.get[0], t.get[1:]...)
	out, err := cmd.Output()
	if err != nil || len(out) > maxClip {
		return "", false
	}
	return string(out), true
}

func (t clipboardTool) write(s string) {
	if len(t.set) == 0 {
		return
	}
	cmd := exec.Command(t.set[0], t.set[1:]...)
	cmd.Stdin = strings.NewReader(s)
	if err := cmd.Run(); err != nil {
		logf("clipboard write failed: %v", err)
	}
}
