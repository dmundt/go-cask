//go:build windows

package persistent

import (
	"fmt"
	"os"
)

func mmapBytes(file *os.File, size int) (bool, []byte, error) {
	if size <= 0 {
		return false, nil, nil
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
	return nil
}
