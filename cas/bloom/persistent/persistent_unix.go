//go:build !windows

package persistent

import (
	"fmt"
	"os"
	"syscall"
)

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
	data, err := syscall.Mmap(int(file.Fd()), 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err == nil {
		return true, data, nil
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
	return syscall.Munmap(data)
}
