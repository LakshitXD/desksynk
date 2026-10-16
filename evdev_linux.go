package main

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	evSyn = 0x00
	evKey = 0x01
	evRel = 0x02

	relX           = 0x00
	relY           = 0x01
	relHWheel      = 0x06
	relWheel       = 0x08
	relWheelHiRes  = 0x0b
	relHWheelHiRes = 0x0c

	keyA     = 30
	keySpace = 57
	btnLeft  = 0x110
	btnTask  = 0x117

	inputEventSize = 24 // struct input_event on 64-bit
)

func iocRead(nr, size uintptr) uintptr { return 2<<30 | size<<16 | 'E'<<8 | nr }

var evioCGrab uintptr = 1<<30 | 4<<16 | 'E'<<8 | 0x90

type inputEvent struct {
	typ, code uint16
	value     int32
}

// device is one /dev/input/event* node that is a keyboard, a mouse, or both.
type device struct {
	path, name string
	f          *os.File
	hiRes      bool
	dx, dy     int32 // relative motion accumulated until SYN_REPORT
}

func ioctl(f *os.File, req, arg uintptr) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func bits(f *os.File, ev uintptr, n int) []byte {
	b := make([]byte, n)
	if ioctl(f, iocRead(0x20+ev, uintptr(n)), uintptr(unsafe.Pointer(&b[0]))) != nil {
		return nil
	}
	return b
}

func hasBit(b []byte, n int) bool { return n/8 < len(b) && b[n/8]&(1<<(n%8)) != 0 }

var errNotInput = errors.New("not a keyboard or mouse")

func openDevice(path string) (*device, error) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	name := make([]byte, 256)
	ioctl(f, iocRead(0x06, 256), uintptr(unsafe.Pointer(&name[0])))
	ev := bits(f, 0, 4)
	keys := bits(f, evKey, 96)
	rels := bits(f, evRel, 4)
	keyboard := hasBit(ev, evKey) && hasBit(keys, keyA) && hasBit(keys, keySpace)
	mouse := hasBit(ev, evRel) && hasBit(rels, relX) && hasBit(rels, relY) && hasBit(keys, btnLeft)
	if !keyboard && !mouse {
		f.Close()
		return nil, errNotInput
	}
	return &device{
		path:  path,
		name:  strings.TrimRight(string(name), "\x00"),
		f:     f,
		hiRes: hasBit(rels, relWheelHiRes),
	}, nil
}

func (d *device) grab(on bool) error {
	var v uintptr
	if on {
		v = 1
	}
	return ioctl(d.f, evioCGrab, v)
}

// read delivers events until the device goes away.
func (d *device) read(out chan<- devEvent) {
	buf := make([]byte, inputEventSize*64)
	for {
		n, err := d.f.Read(buf)
		if err != nil {
			out <- devEvent{dev: d, err: err}
			return
		}
		for p := buf[:n]; len(p) >= inputEventSize; p = p[inputEventSize:] {
			out <- devEvent{dev: d, ev: inputEvent{
				typ:   binary.LittleEndian.Uint16(p[16:]),
				code:  binary.LittleEndian.Uint16(p[18:]),
				value: int32(binary.LittleEndian.Uint32(p[20:])),
			}}
		}
	}
}

type devEvent struct {
	dev *device
	ev  inputEvent
	err error
}

func inputNodes() []string {
	paths, _ := filepath.Glob("/dev/input/event*")
	return paths
}
