//go:build !windows

package persistent

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
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
	for _, prot := range []int{syscall.PROT_READ | syscall.PROT_WRITE, syscall.PROT_READ} {
		data, err := syscall.Mmap(int(file.Fd()), 0, size, prot, syscall.MAP_SHARED)
		if err == nil {
			return true, data, nil
		}
		if prot == syscall.PROT_READ|syscall.PROT_WRITE && err != nil {
			continue
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
	if err := syscall.Munmap(data); err != nil && !errors.Is(err, syscall.EINVAL) {
		return err
	}
	return nil
}

func flushMapped(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	return unix.Msync(data, unix.MS_SYNC)
}

func closeMappedByAddr(addr uintptr, size int) error {
	if size <= 0 || addr == 0 {
		return nil
	}
	if _, ok := mappedViews.Load(addr); !ok {
		return nil
	}
	mappedViews.Delete(addr)
	return nil
}

func flushMappedByAddr(addr uintptr, size int) error {
	if size <= 0 || addr == 0 {
		return nil
	}
	if _, ok := mappedViews.Load(addr); !ok {
		return nil
	}
	return nil
}
