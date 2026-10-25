//go:build windows

package machine

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"runtime"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var gdi32 = windows.NewLazySystemDLL("gdi32.dll")
var sendInput = user32.NewProc("SendInput")
var getDC = user32.NewProc("GetDC")
var releaseDC = user32.NewProc("ReleaseDC")
var metrics = user32.NewProc("GetSystemMetrics")
var createDC = gdi32.NewProc("CreateCompatibleDC")
var createBitmap = gdi32.NewProc("CreateCompatibleBitmap")
var selectObject = gdi32.NewProc("SelectObject")
var bitBlt = gdi32.NewProc("BitBlt")
var getBits = gdi32.NewProc("GetDIBits")
var deleteObject = gdi32.NewProc("DeleteObject")
var deleteDC = gdi32.NewProc("DeleteDC")
var inputDesktop = user32.NewProc("OpenInputDesktop")
var closeDesktop = user32.NewProc("CloseDesktop")
var threadDPI = user32.NewProc("SetThreadDpiAwarenessContext")
var mapVirtualKey = user32.NewProc("MapVirtualKeyW")

type windowsDesktop struct{}

func NewDesktop() (Desktop, error) {
	// Construction has no desktop side effects. The production binary rejects
	// unsupported architectures; GUI calls check the interactive desktop.
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, errors.New("desktop requires 64-bit Windows")
	}
	return windowsDesktop{}, nil
}
func checkDesktop() error {
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil || session == 0 {
		return errors.New("bridge requires a logged-in interactive user session")
	}
	desktop, _, _ := inputDesktop.Call(0, 0, 0x0100|0x0001)
	if desktop == 0 {
		return errors.New("interactive desktop unavailable or locked")
	}
	defer closeDesktop.Call(desktop)
	name := make([]uint16, 256)
	var needed uint32
	ok, _, _ := user32.NewProc("GetUserObjectInformationW").Call(desktop, 2, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)*2), uintptr(unsafe.Pointer(&needed)))
	if ok == 0 || windows.UTF16ToString(name) != "Default" {
		return errors.New("secure or alternate input desktop unavailable")
	}
	return nil
}
func desktopBounds() (int, int, int, int) {
	x, _, _ := metrics.Call(76)
	y, _, _ := metrics.Call(77)
	w, _, _ := metrics.Call(78)
	h, _, _ := metrics.Call(79)
	return int(int32(x)), int(int32(y)), int(w), int(h)
}
func (windowsDesktop) Screenshot(ctx context.Context) ([]byte, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	oldDPI, _, _ := threadDPI.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	if oldDPI == 0 {
		return nil, ErrInvalid
	}
	defer threadDPI.Call(oldDPI)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := checkDesktop(); err != nil {
		return nil, err
	}
	x, y, width, height := desktopBounds()
	if width <= 0 || height <= 0 || width*height > 32<<20 {
		return nil, ErrInvalid
	}
	screen, _, _ := getDC.Call(0)
	if screen == 0 {
		return nil, ErrInvalid
	}
	defer releaseDC.Call(0, screen)
	memory, _, _ := createDC.Call(screen)
	if memory == 0 {
		return nil, ErrInvalid
	}
	defer deleteDC.Call(memory)
	bitmap, _, _ := createBitmap.Call(screen, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, ErrInvalid
	}
	defer deleteObject.Call(bitmap)
	old, _, _ := selectObject.Call(memory, bitmap)
	ok, _, _ := bitBlt.Call(memory, 0, 0, uintptr(width), uintptr(height), screen, uintptr(x), uintptr(y), 0x00CC0020|0x40000000)
	selectObject.Call(memory, old)
	if ok == 0 {
		return nil, ErrInvalid
	}
	type bitmapInfo struct {
		Size                        uint32
		Width, Height               int32
		Planes, BitCount            uint16
		Compression, SizeImage      uint32
		XPels, YPels                int32
		ColorsUsed, ColorsImportant uint32
	}
	info := bitmapInfo{Size: 40, Width: int32(width), Height: -int32(height), Planes: 1, BitCount: 32}
	pixels := make([]byte, width*height*4)
	count, _, _ := getBits.Call(screen, bitmap, 0, uintptr(height), uintptr(unsafe.Pointer(&pixels[0])), uintptr(unsafe.Pointer(&info)), 0)
	if count != uintptr(height) {
		return nil, ErrInvalid
	}
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
		pixels[i+3] = 255
	}
	img := &image.RGBA{Pix: pixels, Stride: width * 4, Rect: image.Rect(0, 0, width, height)}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func keyboard(vk, scan uint16, flags uint32) [40]byte {
	var input [40]byte
	binary.LittleEndian.PutUint32(input[:4], 1)
	binary.LittleEndian.PutUint16(input[8:10], vk)
	binary.LittleEndian.PutUint16(input[10:12], scan)
	binary.LittleEndian.PutUint32(input[12:16], flags)
	return input
}
func mouse(x, y int32, data, flags uint32) [40]byte {
	var input [40]byte
	binary.LittleEndian.PutUint32(input[8:12], uint32(x))
	binary.LittleEndian.PutUint32(input[12:16], uint32(y))
	binary.LittleEndian.PutUint32(input[16:20], data)
	binary.LittleEndian.PutUint32(input[20:24], flags)
	return input
}
func (windowsDesktop) Input(ctx context.Context, actions []Input) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	oldDPI, _, _ := threadDPI.Call(^uintptr(3))
	if oldDPI == 0 {
		return ErrInvalid
	}
	defer threadDPI.Call(oldDPI)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkDesktop(); err != nil {
		return err
	}
	_, _, width, height := desktopBounds()
	events, err := encodeInput(actions, width, height)
	if err != nil {
		return err
	}
	mapInputScans(events, func(key uint16) uint32 {
		scan, _, _ := mapVirtualKey.Call(uintptr(key), 4)
		return uint32(scan)
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	n, _, _ := sendInput.Call(uintptr(len(events)), uintptr(unsafe.Pointer(&events[0])), 40)
	runtime.KeepAlive(events)
	if n != uintptr(len(events)) {
		// Only send releases for this planned batch; never retry input.
		// This is best effort if UIPI/desktop changes blocked delivery.
		releases := inputReleases(events)
		if len(releases) > 0 {
			_, _, _ = sendInput.Call(uintptr(len(releases)), uintptr(unsafe.Pointer(&releases[0])), 40)
			runtime.KeepAlive(releases)
		}
		return errors.New("input delivery incomplete; outcome unknown")
	}
	return nil
}

// Encoding is pure: regression tests never touch a real desktop.
func encodeInput(actions []Input, width, height int) ([][40]byte, error) {
	if width <= 1 || height <= 1 {
		return nil, ErrInvalid
	}
	events := make([][40]byte, 0, len(actions)*2)
	for _, action := range actions {
		if len(action.Modifiers) != 0 && action.Type != "key" {
			return nil, ErrInvalid
		}
		switch action.Type {
		case "move", "click":
			if action.X < 0 || action.Y < 0 || action.X >= width || action.Y >= height {
				return nil, ErrInvalid
			}
			events = append(events, mouse(int32(action.X*65535/(width-1)), int32(action.Y*65535/(height-1)), 0, 0x8000|0x4000|0x0001))
			if action.Type == "click" {
				down, up := uint32(2), uint32(4)
				switch action.Button {
				case "", "left":
				case "right":
					down, up = 8, 16
				case "middle":
					down, up = 32, 64
				default:
					return nil, ErrInvalid
				}
				events = append(events, mouse(0, 0, 0, down), mouse(0, 0, 0, up))
			}
		case "key":
			if action.Key == 0 || action.Key > 254 || len(action.Modifiers) > 4 {
				return nil, ErrInvalid
			}
			modifiers := make([]uint16, 0, len(action.Modifiers))
			for _, name := range action.Modifiers {
				key := map[string]uint16{"ctrl": 0x11, "shift": 0x10, "alt": 0x12, "win": 0x5b}[name]
				if key == 0 || key == action.Key {
					return nil, ErrInvalid
				}
				for _, previous := range modifiers {
					if previous == key {
						return nil, ErrInvalid
					}
				}
				modifiers = append(modifiers, key)
				events = append(events, keyboard(key, 0, 0))
			}
			events = append(events, keyboard(action.Key, 0, 0), keyboard(action.Key, 0, 2))
			for i := len(modifiers) - 1; i >= 0; i-- {
				events = append(events, keyboard(modifiers[i], 0, 2))
			}
		case "text":
			for _, unit := range utf16.Encode([]rune(action.Text)) {
				events = append(events, keyboard(0, unit, 4), keyboard(0, unit, 6))
			}
		case "scroll":
			if action.Delta < -12000 || action.Delta > 12000 {
				return nil, ErrInvalid
			}
			events = append(events, mouse(0, 0, uint32(int32(action.Delta)), 0x0800))
		default:
			return nil, ErrInvalid
		}
		if len(events) > 8192 {
			return nil, ErrInvalid
		}
	}
	if len(events) == 0 {
		return nil, ErrInvalid
	}
	return events, nil
}

func inputReleases(events [][40]byte) [][40]byte {
	var releases [][40]byte
	for _, event := range events {
		if binary.LittleEndian.Uint32(event[:4]) == 1 {
			if binary.LittleEndian.Uint32(event[12:16])&2 != 0 {
				releases = append(releases, event)
			}
		} else if binary.LittleEndian.Uint32(event[20:24])&(4|16|64) != 0 {
			releases = append(releases, event)
		}
	}
	return releases
}

// Keep virtual-key semantics while supplying scan codes for applications
// which inspect them. E0-prefixed keys require EXTENDEDKEY on down and up.
func mapInputScans(events [][40]byte, lookup func(uint16) uint32) {
	for i := range events {
		event := &events[i]
		if binary.LittleEndian.Uint32(event[:4]) != 1 {
			continue
		}
		flags := binary.LittleEndian.Uint32(event[12:16])
		if flags&4 != 0 {
			continue
		} // Unicode scan contains the UTF-16 unit.
		scan := lookup(binary.LittleEndian.Uint16(event[8:10]))
		binary.LittleEndian.PutUint16(event[10:12], uint16(scan&0xff))
		if scan&0xff00 == 0xe000 {
			flags |= 1
		}
		binary.LittleEndian.PutUint32(event[12:16], flags)
	}
}
