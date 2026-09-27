package bounded

import (
	"io"
	"sync"
)

// Compressor is the pooled writer contract: a compression writer plus the Reset
// that re-arms it on a new destination. compress/flate, compress/gzip and
// compress/zlib each expose exactly Reset(io.Writer) on the writer its
// constructor returns, so a wrapper hands its concrete writer over unchanged.
type Compressor interface {
	io.WriteCloser
	Reset(w io.Writer)
}

// Decompressor is the pooled reader contract: a decompressor plus the Reset
// that re-arms it on a new source. compress/gzip's *Reader matches it directly;
// compress/flate and compress/zlib take a preset dictionary instead, so those
// two wrappers carry the two-line adapter that supplies nil.
type Decompressor interface {
	io.ReadCloser
	Reset(r io.Reader) error
}

// WriterPool reuses compression writers across Encode calls. A flate-family
// writer carries roughly a megabyte of match tables and a 32 KiB window, so
// building one per call spent ~1.1 MB to store a 64-byte object (go-cask#378);
// a pooled writer is Reset onto this call's destination instead.
//
// Safe for concurrent use: the pool is the synchronization, a writer belongs to
// one goroutine from get to put, and a parked writer holds io.Discard, so no
// call can observe another call's destination. The pool carries no state a call
// can observe: it is scratch, never configuration.
type WriterPool struct {
	new  func(io.Writer) (Compressor, error)
	pool sync.Pool
}

// NewWriterPool returns a pool of writers built by newWriter. newWriter runs
// only when the pool is empty — a reused writer is re-armed through
// Compressor.Reset, never rebuilt.
func NewWriterPool(newWriter func(io.Writer) (Compressor, error)) *WriterPool {
	return &WriterPool{new: newWriter}
}

// get returns a writer sending its output to dst.
func (p *WriterPool) get(dst io.Writer) (Compressor, error) {
	if v := p.pool.Get(); v != nil {
		w := v.(Compressor)
		w.Reset(dst)
		return w, nil
	}
	return p.new(dst)
}

// put parks w on io.Discard and returns it to the pool. Only a writer whose
// call succeeded is parked: one that failed mid-stream is dropped instead, so a
// poisoned writer can never produce the next call's bytes.
func (p *WriterPool) put(w Compressor) {
	w.Reset(io.Discard)
	p.pool.Put(w)
}

// ReaderPool reuses decompressors across Decode calls, the read-path half of
// WriterPool's argument: rebuilding a flate-family reader per call allocated
// ~41 KB to inflate one small object (go-cask#378). It carries the same
// concurrency contract: the pool is the synchronization, a reader belongs to
// one goroutine from get to put, and a parked reader holds an empty source.
type ReaderPool struct {
	new  func(io.Reader) (Decompressor, error)
	pool sync.Pool
}

// NewReaderPool returns a pool of readers built by newReader. newReader runs
// only when the pool is empty — a reused reader is re-armed through
// Decompressor.Reset, never rebuilt.
func NewReaderPool(newReader func(io.Reader) (Decompressor, error)) *ReaderPool {
	return &ReaderPool{new: newReader}
}

// get returns a reader reading src. A pooled reader that cannot be re-armed on
// src reports that failure and is dropped rather than parked: the failure is
// src's own header error — what building the reader afresh would report — not a
// pool condition.
func (p *ReaderPool) get(src io.Reader) (Decompressor, error) {
	if v := p.pool.Get(); v != nil {
		d := v.(Decompressor)
		if err := d.Reset(src); err != nil {
			return nil, err
		}
		return d, nil
	}
	return p.new(src)
}

// put parks d on an empty source and returns it to the pool. Only a reader
// whose stream was read to the end is parked: one that failed mid-stream is
// dropped instead, so a damaged payload cannot leave the next call a damaged
// reader.
func (p *ReaderPool) put(d Decompressor) {
	_ = d.Reset(eofReader{})
	p.pool.Put(d)
}

// eofReader is the source a parked reader holds. It reports end of stream
// immediately, so a pooled reader pins no payload between calls, and it
// implements io.ByteReader so a decompressor adopts it directly instead of
// wrapping it in the buffered reader a plain io.Reader source would allocate
// per call. Its header parse fails by design; re-arming the reader on real data
// replaces that state, because every decompressor clears the previous parse
// before reading the next header.
type eofReader struct{}

// eofReader must satisfy io.ByteReader for a decompressor to adopt it directly:
// without it, makeReader wraps the source in a bufio.Reader and allocates one
// per parked call.
var _ io.ByteReader = eofReader{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

func (eofReader) ReadByte() (byte, error) { return 0, io.EOF }
