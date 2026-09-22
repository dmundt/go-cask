package persistent

import (
	"errors"
	"fmt"
	"os"
)

// mmapDriver is the platform-specific memory-mapping implementation a Filter
// uses. It is an immutable value: production code always runs with the driver
// returned by defaultMmapDriver, and tests inject a fake one through newFilter.
// Keeping it on the Filter instead of in a mutable package variable means one
// test's stub can never leak into another test or into production code.
type mmapDriver struct {
	// mmapBytes opens a writable view of size bytes of file. The bool result
	// reports whether the returned slice is a real mapping (and therefore must
	// be released with closeMapped) rather than a heap fallback buffer.
	mmapBytes func(file *os.File, size int) (bool, []byte, error)
	// closeMapped releases a slice previously returned by mmapBytes with a true
	// mapping result. It is a no-op for a heap fallback buffer.
	closeMapped func(data []byte) error
	// flushMapped writes a mapped slice back to its backing file.
	flushMapped func(data []byte) error
}

// defaultMmapDriver returns the driver backed by the platform mmap
// implementation. It is a function rather than a package variable so the driver
// stays immutable and there is no mutable global state to reset between tests.
func defaultMmapDriver() mmapDriver {
	return mmapDriver{
		mmapBytes:   mmapBytes,
		closeMapped: closeMapped,
		flushMapped: flushMapped,
	}
}

// mapFile opens the backing view for file. A Filter built without an explicit
// driver cannot map anything, so it reports an error instead of panicking on a
// nil function call.
func (d mmapDriver) mapFile(file *os.File, size int) (bool, []byte, error) {
	if d.mmapBytes == nil {
		return false, nil, errors.New("no memory-mapping driver configured")
	}
	return d.mmapBytes(file, size)
}

// unmap releases a view. A nil function is treated as "nothing to release" so a
// zero-value driver on a hand-built Filter degrades to a no-op rather than a
// nil-func panic.
func (d mmapDriver) unmap(data []byte) error {
	if d.closeMapped == nil {
		return nil
	}
	return d.closeMapped(data)
}

// flush writes a view back to its backing file. As with unmap, a nil function
// degrades to a no-op.
func (d mmapDriver) flush(data []byte) error {
	if d.flushMapped == nil {
		return nil
	}
	return d.flushMapped(data)
}

// readPadded reads the current contents of file and returns them padded with
// zero bytes (or truncated) to exactly size bytes. It backs the heap fallback
// used when a real mapping is unavailable.
func readPadded(file *os.File, size int) ([]byte, error) {
	buf, err := os.ReadFile(file.Name())
	if err != nil {
		return nil, fmt.Errorf("bloom/persistent: read persistent file: %w", err)
	}
	if len(buf) < size {
		buf = append(buf, make([]byte, size-len(buf))...)
	}
	return buf[:size], nil
}
