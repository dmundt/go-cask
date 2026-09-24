package sidecar

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dmundt/go-cask/cas"
)

// VerifyReport summarizes a VerifyAll pass.
type VerifyReport struct {
	// Checked is the number of records validated.
	Checked int
	// Bad lists objects whose stored bytes failed their recorded checksum or
	// size. It reports confirmed corruption only: a digest that could not be
	// checked for another reason aborts the pass instead of appearing here,
	// and an object without a record is Unrecorded, never Bad.
	Bad []cas.Digest
	// Unrecorded lists objects that have no record. They are listed, never
	// examined: only a writer that read the bytes can record a checksum.
	Unrecorded []cas.Digest
}

// Verifier checks stored bytes against their sidecar record. It is bound to one
// checksum name and hasher, which need not be the algorithm the record was
// written with: asking for a different one is reported as
// ErrChecksumAlgorithm rather than as corruption.
type Verifier struct {
	backend *Backend
	algo    string
	hasher  cas.Hasher
}

// Verifier returns a Verifier for this record store. There is no registry: the
// caller names the algorithm (for example crc32.Name) and supplies its hasher,
// exactly as it does for the addressing hasher.
func (b *Backend) Verifier(algo string, hasher cas.Hasher) *Verifier {
	return &Verifier{backend: b, algo: algo, hasher: hasher}
}

// Verify checks the stored bytes at d against the checksum recorded for d.
//
// It deliberately does not re-check the object's address: identity is the
// caller's strong hasher's job (cas.Verify). This method answers the cheaper
// question — are the stored bytes still the ones the record describes? — and
// its failures are:
//
//   - ErrUnrecorded (which wraps cas.ErrNotFound) when d has no record: an
//     unchecked object, never corruption;
//   - ErrChecksumAlgorithm when the record was written under another checksum;
//   - cas.ErrCorrupt when the stored size or the recomputed checksum disagrees
//     with the record, or when the record itself cannot be read.
func (v *Verifier) Verify(ctx context.Context, d cas.Digest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v == nil || v.backend == nil {
		return fmt.Errorf("sidecar: verify: nil backend")
	}
	if err := cas.CheckDigest(d, "sidecar: verify"); err != nil {
		return err
	}
	if v.hasher == nil {
		return fmt.Errorf("sidecar: verify: nil hasher")
	}
	if v.algo == "" {
		return fmt.Errorf("sidecar: verify: no checksum algorithm")
	}
	rec, found, err := v.backend.readRecord(d)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrUnrecorded, d)
	}
	return v.verifyRecord(ctx, rec)
}

// VerifyAll checks every stored object that has a record, using only the
// minimal Backend interface (List, Get), so it works against any backend. An
// object without a record is reported in Unrecorded rather than counted as bad;
// a checksum failure is reported in Bad; anything else — a damaged record, a
// wrong algorithm, a read failure — stops the pass with the error, because it
// says the pass could not be trusted rather than that one object is damaged.
func (v *Verifier) VerifyAll(ctx context.Context) (*VerifyReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if v == nil || v.backend == nil {
		return nil, fmt.Errorf("sidecar: verify all: nil backend")
	}
	if v.hasher == nil {
		return nil, fmt.Errorf("sidecar: verify all: nil hasher")
	}
	if v.algo == "" {
		return nil, fmt.Errorf("sidecar: verify all: no checksum algorithm")
	}
	digests, err := v.backend.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	report := &VerifyReport{}
	for _, d := range digests {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		rec, found, err := v.backend.readRecord(d)
		if err != nil {
			return report, err
		}
		if !found {
			report.Unrecorded = append(report.Unrecorded, d)
			continue
		}
		report.Checked++
		if err := v.verifyRecord(ctx, rec); err != nil {
			if errors.Is(err, cas.ErrCorrupt) {
				report.Bad = append(report.Bad, d)
				continue
			}
			return report, fmt.Errorf("sidecar: verify all %s: %w", d, err)
		}
	}
	return report, nil
}

// verifyRecord re-reads the object the record describes and compares its size
// and checksum with the recorded values.
func (v *Verifier) verifyRecord(ctx context.Context, rec *Record) error {
	if rec.ChecksumAlgo != v.algo {
		return fmt.Errorf("%w: record %s names %q, reader configured with %q", ErrChecksumAlgorithm, rec.Digest, rec.ChecksumAlgo, v.algo)
	}
	rc, err := v.backend.inner.Get(ctx, rec.Digest)
	if err != nil {
		return err
	}
	counted := &countingReader{r: rc}
	sum, err := v.hasher.Digest(counted)
	closeErr := rc.Close()
	if err != nil {
		return fmt.Errorf("sidecar: verify %s: %w", rec.Digest, err)
	}
	if closeErr != nil {
		return fmt.Errorf("sidecar: verify %s: close: %w", rec.Digest, closeErr)
	}
	if counted.n != rec.Size {
		return fmt.Errorf("%w: %s: stored size %d, record says %d", cas.ErrCorrupt, rec.Digest, counted.n, rec.Size)
	}
	if !sum.Equal(rec.Checksum) {
		return fmt.Errorf("%w: %s: %s checksum mismatch", cas.ErrCorrupt, rec.Digest, v.algo)
	}
	return nil
}

// countingReader counts the bytes read through it, so a stored size can be
// compared with the record without buffering the object.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
