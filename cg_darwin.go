package main

import (
	"fmt"

	"github.com/ebitengine/purego"
)

// CoreGraphics bindings through purego, so the Mac binary builds without cgo
// and can be cross-compiled.

type cgPoint struct{ X, Y float64 }
type cgSize struct{ W, H float64 }
type cgRect struct {
	Origin cgPoint
	Size   cgSize
}

const (
	cgHIDEventTap           = 0
	cgEventSourceStateHID   = 1
	cgScrollUnitLine        = 1
	cgFieldClickState       = 1
	cgFieldButtonNumber     = 3
	cgFieldDeltaX           = 4
	cgFieldDeltaY           = 5
	cgFieldKeyboardAutorept = 8

	cgLeftMouseDown     = 1
	cgLeftMouseUp       = 2
	cgRightMouseDown    = 3
	cgRightMouseUp      = 4
	cgMouseMoved        = 5
	cgLeftMouseDragged  = 6
	cgRightMouseDragged = 7
	cgFlagsChanged      = 12
	cgOtherMouseDown    = 25
	cgOtherMouseUp      = 26
	cgOtherMouseDragged = 27

	flagAlphaShift = 0x00010000
	flagShift      = 0x00020000
	flagControl    = 0x00040000
	flagOption     = 0x00080000
	flagCommand    = 0x00100000
	flagNumPad     = 0x00200000
	flagFn         = 0x00800000
)

var (
	cfRelease                   func(uintptr)
	cgEventSourceCreate         func(int32) uintptr
	cgEventSourceSetSuppression func(uintptr, float64)
	cgEventCreate               func(uintptr) uintptr
	cgEventGetLocation          func(uintptr) cgPoint
	cgEventCreateMouseEvent     func(uintptr, uint32, cgPoint, uint32) uintptr
	cgEventCreateKeyboardEvent  func(uintptr, uint16, bool) uintptr
	cgEventCreateScrollWheel2   func(uintptr, uint32, uint32, int32, int32, int32) uintptr
	cgEventSetIntegerValueField func(uintptr, uint32, int64)
	cgEventSetFlags             func(uintptr, uint64)
	cgEventSetType              func(uintptr, uint32)
	cgEventPost                 func(uint32, uintptr)
	cgGetActiveDisplayList      func(uint32, *uint32, *uint32) int32
	cgDisplayBounds             func(uint32) cgRect
	axIsProcessTrusted          func() bool
)

func loadCoreGraphics() error {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("load CoreFoundation: %w", err)
	}
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("load CoreGraphics: %w", err)
	}
	as, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("load ApplicationServices: %w", err)
	}
	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	for name, fn := range map[string]any{
		"CGEventSourceCreate":                            &cgEventSourceCreate,
		"CGEventSourceSetLocalEventsSuppressionInterval": &cgEventSourceSetSuppression,
		"CGEventCreate":                                  &cgEventCreate,
		"CGEventGetLocation":                             &cgEventGetLocation,
		"CGEventCreateMouseEvent":                        &cgEventCreateMouseEvent,
		"CGEventCreateKeyboardEvent":                     &cgEventCreateKeyboardEvent,
		"CGEventCreateScrollWheelEvent2":                 &cgEventCreateScrollWheel2,
		"CGEventSetIntegerValueField":                    &cgEventSetIntegerValueField,
		"CGEventSetFlags":                                &cgEventSetFlags,
		"CGEventSetType":                                 &cgEventSetType,
		"CGEventPost":                                    &cgEventPost,
		"CGGetActiveDisplayList":                         &cgGetActiveDisplayList,
		"CGDisplayBounds":                                &cgDisplayBounds,
	} {
		purego.RegisterLibFunc(fn, cg, name)
	}
	purego.RegisterLibFunc(&axIsProcessTrusted, as, "AXIsProcessTrusted")
	return nil
}

func displays() []cgRect {
	var ids [16]uint32
	var n uint32
	if cgGetActiveDisplayList(uint32(len(ids)), &ids[0], &n) != 0 {
		return nil
	}
	rs := make([]cgRect, n)
	for i := range rs {
		rs[i] = cgDisplayBounds(ids[i])
	}
	return rs
}

func (r cgRect) contains(x, y float64) bool {
	return x >= r.Origin.X && x < r.Origin.X+r.Size.W && y >= r.Origin.Y && y < r.Origin.Y+r.Size.H
}

func cursorLocation() cgPoint {
	ev := cgEventCreate(0)
	defer cfRelease(ev)
	return cgEventGetLocation(ev)
}
