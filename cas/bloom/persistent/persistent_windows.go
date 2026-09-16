//go:build windows

package persistent

import (
	"fmt"
	"math"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var mappedViews sync.Map

type windowsOSHooks struct {
	utf16PtrFromString func(string) (*uint16, error)
	createFile         func(*uint16, uint32, uint32, *windows.SecurityAttributes, uint32, uint32, windows.Handle) (windows.Handle, error)
	closeHandle        func(windows.Handle) error
	createFileMapping  func(windows.Handle, *windows.SecurityAttributes, uint32, uint32, uint32, *uint16) (windows.Handle, error)
	mapViewOfFile      func(windows.Handle, uint32, uint32, uint32, uintptr) (uintptr, error)
	unmapViewOfFile    func(uintptr) error
	flushViewOfFile    func(uintptr, uintptr) error
}

var defaultWindowsOSHooks = windowsOSHooks{
	utf16PtrFromString: windows.UTF16PtrFromString,
	createFile: func(name *uint16, access, shareMode uint32, sa *windows.SecurityAttributes, creationDisposition, flagsAndAttributes uint32, templateFile windows.Handle) (windows.Handle, error) {
		return windows.CreateFile(name, access, shareMode, sa, creationDisposition, flagsAndAttributes, templateFile)
	},
	closeHandle: func(h windows.Handle) error {
		return windows.CloseHandle(h)
	},
	createFileMapping: func(file windows.Handle, sa *windows.SecurityAttributes, protect, maxSizeHigh, maxSizeLow uint32, name *uint16) (windows.Handle, error) {
		return windows.CreateFileMapping(file, sa, protect, maxSizeHigh, maxSizeLow, name)
	},
	mapViewOfFile: func(mapping windows.Handle, access, offsetHigh, offsetLow uint32, numBytes uintptr) (uintptr, error) {
		return windows.MapViewOfFile(mapping, access, offsetHigh, offsetLow, numBytes)
	},
	unmapViewOfFile: windows.UnmapViewOfFile,
	flushViewOfFile: windows.FlushViewOfFile,
}

var windowsOS = defaultWindowsOSHooks

func mmapBytes(file *os.File, size int) (bool, []byte, error) {
	if size <= 0 {
		return false, nil, nil
	}
	if fi, err := file.Stat(); err == nil && fi.Size() < int64(size) {
		buf, readErr := os.ReadFile(file.Name())
		if readErr != nil {
			return false, nil, fmt.Errorf("bloom: read persistent file: %w", readErr)
		}
		if len(buf) < size {
			buf = append(buf, make([]byte, size-len(buf))...)
		}
		return false, buf[:size], nil
	}
	if size > math.MaxUint32 {
		buf, readErr := os.ReadFile(file.Name())
		if readErr != nil {
			return false, nil, fmt.Errorf("bloom: read persistent file: %w", readErr)
		}
		if len(buf) < size {
			buf = append(buf, make([]byte, size-len(buf))...)
		}
		return false, buf[:size], nil
	}

	path, err := windowsOS.utf16PtrFromString(file.Name())
	if err != nil {
		buf, readErr := os.ReadFile(file.Name())
		if readErr != nil {
			return false, nil, fmt.Errorf("bloom: read persistent file: %w", readErr)
		}
		if len(buf) < size {
			buf = append(buf, make([]byte, size-len(buf))...)
		}
		return false, buf[:size], nil
	}

	handle, err := windowsOS.createFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		buf, readErr := os.ReadFile(file.Name())
		if readErr != nil {
			return false, nil, fmt.Errorf("bloom: read persistent file: %w", readErr)
		}
		if len(buf) < size {
			buf = append(buf, make([]byte, size-len(buf))...)
		}
		return false, buf[:size], nil
	}
	defer windowsOS.closeHandle(handle)

	mapping, err := windowsOS.createFileMapping(handle, nil, windows.PAGE_READWRITE, 0, uint32(size), nil)
	if err != nil {
		buf, readErr := os.ReadFile(file.Name())
		if readErr != nil {
			return false, nil, fmt.Errorf("bloom: read persistent file: %w", readErr)
		}
		if len(buf) < size {
			buf = append(buf, make([]byte, size-len(buf))...)
		}
		return false, buf[:size], nil
	}
	defer windowsOS.closeHandle(mapping)

	addr, err := windowsOS.mapViewOfFile(mapping, windows.FILE_MAP_READ|windows.FILE_MAP_WRITE, 0, 0, uintptr(size))
	if err != nil {
		buf, readErr := os.ReadFile(file.Name())
		if readErr != nil {
			return false, nil, fmt.Errorf("bloom: read persistent file: %w", readErr)
		}
		if len(buf) < size {
			buf = append(buf, make([]byte, size-len(buf))...)
		}
		return false, buf[:size], nil
	}

	view := unsafe.Slice((*byte)(unsafe.Pointer(addr)), size)
	mappedViews.Store(uintptr(unsafe.Pointer(&view[0])), true)
	return true, view, nil
}

func closeMapped(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	ptr := uintptr(unsafe.Pointer(&data[0]))
	if _, ok := mappedViews.Load(ptr); !ok {
		return nil
	}
	mappedViews.Delete(ptr)
	return windowsOS.unmapViewOfFile(ptr)
}

func flushMapped(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	ptr := uintptr(unsafe.Pointer(&data[0]))
	if _, ok := mappedViews.Load(ptr); !ok {
		return nil
	}
	return windowsOS.flushViewOfFile(ptr, uintptr(len(data)))
}

func closeMappedByAddr(addr uintptr, size int) error {
	if size <= 0 || addr == 0 {
		return nil
	}
	if _, ok := mappedViews.Load(addr); !ok {
		return nil
	}
	mappedViews.Delete(addr)
	return windowsOS.unmapViewOfFile(addr)
}

func flushMappedByAddr(addr uintptr, size int) error {
	if size <= 0 || addr == 0 {
		return nil
	}
	if _, ok := mappedViews.Load(addr); !ok {
		return nil
	}
	return windowsOS.flushViewOfFile(addr, uintptr(size))
}
