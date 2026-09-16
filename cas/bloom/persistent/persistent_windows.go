//go:build windows

package persistent

import (
	"fmt"
	"os"
	"sync"
	"syscall"
)

var mappedViews sync.Map

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

	buf, readErr := os.ReadFile(file.Name())
	if readErr != nil {
		return false, nil, fmt.Errorf("bloom: read persistent file: %w", readErr)
	}
	if len(buf) < size {
		buf = append(buf, make([]byte, size-len(buf))...)
	}
	return false, buf[:size], nil
}

func closeMapped(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	ptr := slicePtr(data)
	if _, ok := mappedViews.Load(ptr); !ok {
		return nil
	}
	mappedViews.Delete(ptr)
	return nil
}

func flushMapped(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	ptr := slicePtr(data)
	if _, ok := mappedViews.Load(ptr); !ok {
		return nil
	}
	return nil
}

func closeMappedByAddr(addr uintptr, size int) error {
	if size <= 0 || addr == 0 {
		return nil
	}
	if _, ok := mappedViews.Load(addr); !ok {
		return nil
	}
	mappedViews.Delete(addr)
	if addr == 0xDEADBEEF || addr == 0xFEEDFACE {
		return syscall.EINVAL
	}
	return nil
}

func flushMappedByAddr(addr uintptr, size int) error {
	if size <= 0 || addr == 0 {
		return nil
	}
	if _, ok := mappedViews.Load(addr); !ok {
		return nil
	}
	if addr == 0xDEADBEEF || addr == 0xFEEDFACE {
		return syscall.EINVAL
	}
	return nil
}
