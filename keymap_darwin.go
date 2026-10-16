package main

// evdevToMac maps Linux evdev keycodes to macOS virtual keycodes (kVK_*).
var evdevToMac = map[uint16]uint16{
	1: 0x35,                                                                                    // Esc
	2: 0x12, 3: 0x13, 4: 0x14, 5: 0x15, 6: 0x17, 7: 0x16, 8: 0x1a, 9: 0x1c, 10: 0x19, 11: 0x1d, // 1..0
	12: 0x1b, 13: 0x18, 14: 0x33, 15: 0x30, // - = Backspace Tab
	16: 0x0c, 17: 0x0d, 18: 0x0e, 19: 0x0f, 20: 0x11, 21: 0x10, 22: 0x20, 23: 0x22, 24: 0x1f, 25: 0x23, // q..p
	26: 0x21, 27: 0x1e, 28: 0x24, // [ ] Enter
	29: 0x3b,                                                                                 // LCtrl
	30: 0x00, 31: 0x01, 32: 0x02, 33: 0x03, 34: 0x05, 35: 0x04, 36: 0x26, 37: 0x28, 38: 0x25, // a..l
	39: 0x29, 40: 0x27, 41: 0x32, // ; ' `
	42: 0x38, 43: 0x2a, // LShift Backslash
	44: 0x06, 45: 0x07, 46: 0x08, 47: 0x09, 48: 0x0b, 49: 0x2d, 50: 0x2e, // z..m
	51: 0x2b, 52: 0x2f, 53: 0x2c, // , . /
	54: 0x3c, 55: 0x43, 56: 0x3a, 57: 0x31, 58: 0x39, // RShift KP* LAlt Space CapsLock
	59: 0x7a, 60: 0x78, 61: 0x63, 62: 0x76, 63: 0x60, 64: 0x61, 65: 0x62, 66: 0x64, 67: 0x65, 68: 0x6d, // F1..F10
	69: 0x47,                                                                       // NumLock -> Keypad Clear
	71: 0x59, 72: 0x5b, 73: 0x5c, 74: 0x4e, 75: 0x56, 76: 0x57, 77: 0x58, 78: 0x45, // KP7 8 9 - 4 5 6 +
	79: 0x53, 80: 0x54, 81: 0x55, 82: 0x52, 83: 0x41, // KP1 2 3 0 .
	86: 0x0a, 87: 0x67, 88: 0x6f, // 102nd F11 F12
	96: 0x4c, 97: 0x3e, 98: 0x4b, 100: 0x3d, // KPEnter RCtrl KP/ RAlt
	102: 0x73, 103: 0x7e, 104: 0x74, 105: 0x7b, 106: 0x7c, 107: 0x77, 108: 0x7d, 109: 0x79, // Home Up PgUp Left Right End Down PgDn
	110: 0x72, 111: 0x75, // Insert->Help Delete
	113: 0x4a, 114: 0x49, 115: 0x48, // Mute VolDown VolUp
	117: 0x51,            // KP=
	125: 0x37, 126: 0x36, // LMeta RMeta -> Command
	127: 0x6e,                                                                              // Compose -> ContextMenu
	183: 0x69, 184: 0x6b, 185: 0x71, 186: 0x6a, 187: 0x40, 188: 0x4f, 189: 0x50, 190: 0x5a, // F13..F20
}

// modifierFlag gives the CGEventFlags bit a modifier key holds down.
var modifierFlag = map[uint16]uint64{
	0x38: flagShift, 0x3c: flagShift,
	0x3b: flagControl, 0x3e: flagControl,
	0x3a: flagOption, 0x3d: flagOption,
	0x37: flagCommand, 0x36: flagCommand,
}

// keyFlags are the extra flags real Mac keyboards attach to these keys; some
// apps (text fields, terminals) look for them.
func keyFlags(mac uint16) uint64 {
	switch {
	case mac >= 0x7b && mac <= 0x7e:
		return flagNumPad | flagFn
	case mac >= 0x72 && mac <= 0x79, mac >= 0x60 && mac <= 0x6f, mac == 0x7a:
		return flagFn
	case mac == 0x41, mac == 0x43, mac == 0x45, mac == 0x47, mac == 0x4b, mac == 0x4c, mac == 0x4e,
		mac >= 0x51 && mac <= 0x5c:
		return flagNumPad
	}
	return 0
}
