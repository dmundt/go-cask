//go:build windows

package persistent

import "os"

// mmapBytes reports a heap fallback buffer rather than a mapping: this package
// has no Windows memory-mapping implementation (CreateFileMapping/MapViewOfFile)
// yet, so a Windows filter reads the whole file into memory, works on that
// buffer, and writes it back from Sync or Close. The contract is otherwise
// identical, which is why the fallback is silent at the call site but visible
// through Filter.IsMapped.
func mmapBytes(file *os.File, size int) (bool, []byte, error) {
	if size <= 0 {
		return false, nil, nil
	}
	buf, err := readPadded(file, size)
	if err != nil {
		return false, nil, err
	}
	return false, buf, nil
}

// closeMapped is a no-op on Windows because mmapBytes never returns a mapping.
func closeMapped(data []byte) error {
	return nil
}

// flushMapped is a no-op on Windows because mmapBytes never returns a mapping;
// the heap buffer is written back by Filter.Sync and Filter.Close instead.
func flushMapped(data []byte) error {
	return nil
}
