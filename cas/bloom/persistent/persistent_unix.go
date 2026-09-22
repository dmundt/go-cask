//go:build !windows

package persistent

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// mmapBytes maps size bytes of file as a shared view. The mapping is requested
// read-write first; a read-only handle (never produced by NewFilter, which opens
// the file O_RDWR) degrades to a shared read-only mapping. When neither mapping
// succeeds, or the file is still shorter than size, the contents are read into a
// heap buffer padded to size and a false mapping result is reported.
func mmapBytes(file *os.File, size int) (bool, []byte, error) {
	if size <= 0 {
		return false, nil, nil
	}
	if fi, err := file.Stat(); err == nil && fi.Size() < int64(size) {
		buf, readErr := readPadded(file, size)
		if readErr != nil {
			return false, nil, readErr
		}
		return false, buf, nil
	}
	for _, prot := range []int{syscall.PROT_READ | syscall.PROT_WRITE, syscall.PROT_READ} {
		data, err := syscall.Mmap(int(file.Fd()), 0, size, prot, syscall.MAP_SHARED)
		if err == nil {
			return true, data, nil
		}
	}
	buf, readErr := readPadded(file, size)
	if readErr != nil {
		return false, nil, readErr
	}
	return false, buf, nil
}

// closeMapped releases a mapping returned by mmapBytes. The caller must only
// pass a slice for which mmapBytes reported a true mapping result.
func closeMapped(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := syscall.Munmap(data); err != nil {
		return fmt.Errorf("bloom/persistent: unmap persistent file: %w", err)
	}
	return nil
}

// flushMapped writes a mapping back to its backing file.
func flushMapped(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if err := unix.Msync(data, unix.MS_SYNC); err != nil {
		return fmt.Errorf("bloom/persistent: flush persistent file: %w", err)
	}
	return nil
}
