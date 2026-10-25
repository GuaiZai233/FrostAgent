//go:build windows

package machine

import (
	"encoding/binary"
	"testing"
)

func TestInputEncodingChordAndReleaseOnlyCleanup(t *testing.T) {
	events, err := encodeInput([]Input{{Type: "key", Key: 'C', Modifiers: []string{"ctrl", "shift"}}, {Type: "click", X: 100, Y: 100}}, 1920, 1080)
	if err != nil || len(events) != 9 {
		t.Fatalf("encoding: %v %d", err, len(events))
	}
	wantKeys := []uint16{0x11, 0x10, 'C', 'C', 0x10, 0x11}
	for i, key := range wantKeys {
		flags := uint32(0)
		if i >= 3 {
			flags = 2
		}
		if binary.LittleEndian.Uint16(events[i][8:10]) != key || binary.LittleEndian.Uint32(events[i][12:16]) != flags {
			t.Fatalf("key %d order/flags invalid", i)
		}
	}
	releases := inputReleases(events)
	if len(releases) != 4 {
		t.Fatalf("cleanup contains %d events", len(releases))
	}
	for _, event := range releases {
		if binary.LittleEndian.Uint32(event[:4]) == 1 {
			if binary.LittleEndian.Uint32(event[12:16])&2 == 0 {
				t.Fatal("cleanup presses key")
			}
		} else if binary.LittleEndian.Uint32(event[20:24]) != 4 {
			t.Fatal("cleanup includes mouse movement/press")
		}
	}
}

func TestInputEncodingUnicodeCoordinatesAndValidation(t *testing.T) {
	events, err := encodeInput([]Input{{Type: "text", Text: "🦊"}, {Type: "move", X: 1919, Y: 1079}}, 1920, 1080)
	if err != nil || len(events) != 5 {
		t.Fatalf("encoding: %v %d", err, len(events))
	}
	if binary.LittleEndian.Uint16(events[0][10:12]) != 0xd83e || binary.LittleEndian.Uint16(events[2][10:12]) != 0xdd8a {
		t.Fatal("UTF-16 surrogate encoding")
	}
	if binary.LittleEndian.Uint32(events[4][8:12]) != 65535 || binary.LittleEndian.Uint32(events[4][12:16]) != 65535 {
		t.Fatal("virtual desktop coordinate mapping")
	}
	for _, action := range []Input{
		{Type: "key", Key: 255}, {Type: "key", Key: 'A', Modifiers: []string{"ctrl", "ctrl"}},
		{Type: "key", Key: 'A', Modifiers: []string{"unknown"}}, {Type: "text", Text: "a", Modifiers: []string{"ctrl"}},
		{Type: "click", X: -1}, {Type: "click", X: 1920}, {Type: "scroll", Delta: 12001},
	} {
		if _, err := encodeInput([]Input{action}, 1920, 1080); err == nil {
			t.Fatalf("invalid action accepted: %+v", action)
		}
	}
}

func TestInputEncodingExtendedScanCodesPreservedByCleanup(t *testing.T) {
	for key, scan := range map[uint16]uint32{0x2e: 0xe053, 0x24: 0xe047, 0x25: 0xe04b, 0xa3: 0xe01d, 0xa5: 0xe038, 0x5b: 0xe05b} {
		events, err := encodeInput([]Input{{Type: "key", Key: key}, {Type: "text", Text: "a"}}, 1920, 1080)
		if err != nil {
			t.Fatal(err)
		}
		mapInputScans(events, func(vk uint16) uint32 {
			if vk != key {
				t.Fatal("Unicode unexpectedly remapped")
			}
			return scan
		})
		for i, flag := range []uint32{1, 3} {
			if binary.LittleEndian.Uint16(events[i][10:12]) != uint16(scan&0xff) || binary.LittleEndian.Uint32(events[i][12:16]) != flag {
				t.Fatalf("VK %x missing extended down/up scan", key)
			}
		}
		if binary.LittleEndian.Uint16(events[2][10:12]) != 'a' || binary.LittleEndian.Uint32(events[2][12:16]) != 4 {
			t.Fatal("Unicode changed")
		}
		if binary.LittleEndian.Uint32(inputReleases(events)[0][12:16]) != 3 {
			t.Fatal("cleanup lost extended flag")
		}
	}
}
