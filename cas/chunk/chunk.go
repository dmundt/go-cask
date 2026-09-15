// Package chunk provides helpers for splitting and reassembling large byte
// payloads into fixed-size chunks. It is a workload-oriented utility layer and
// does not change the content-addressed storage semantics in the core.
package chunk

import "bytes"

// Split breaks data into fixed-size chunks. The final chunk may be shorter.
func Split(data []byte, size int) [][]byte {
	if size <= 0 {
		return [][]byte{append([]byte(nil), data...)}
	}
	if len(data) == 0 {
		return nil
	}
	count := (len(data) + size - 1) / size
	out := make([][]byte, 0, count)
	for i := 0; i < len(data); i += size {
		end := i + size
		if end > len(data) {
			end = len(data)
		}
		out = append(out, append([]byte(nil), data[i:end]...))
	}
	return out
}

// Join reassembles chunk data back into one byte slice.
func Join(chunks [][]byte) []byte {
	if len(chunks) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, c := range chunks {
		buf.Write(c)
	}
	return buf.Bytes()
}

// Count returns the number of chunks produced for a payload of size bytes.
func Count(size, chunkSize int) int {
	if chunkSize <= 0 {
		return 1
	}
	if size <= 0 {
		return 0
	}
	return (size + chunkSize - 1) / chunkSize
}
