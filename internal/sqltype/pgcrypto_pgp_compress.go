package sqltype

import (
	"compress/flate"
	"compress/zlib"
	"context"
	"io"
)

// Scratch grows only from actual bytes. Retired backing arrays are erased,
// including spare capacity; no untrusted decompressed size or aggregate cap.
type pgpScratch struct{ data []byte }

func (s *pgpScratch) clear() { clear(s.data[:cap(s.data)]); s.data = nil }
func (s *pgpScratch) append(data []byte) error {
	maxInt := int(^uint(0) >> 1)
	if len(data) > maxInt-len(s.data) {
		return pgcryptoError("54000", "OpenPGP packet is too large")
	}
	end := len(s.data) + len(data)
	if end > cap(s.data) {
		size := end
		if cap(s.data) <= maxInt/2 {
			size = max(size, 2*cap(s.data))
		}
		next := make([]byte, end, size)
		copy(next, s.data)
		s.clear()
		s.data = next
	} else {
		s.data = s.data[:end]
	}
	copy(s.data[end-len(data):], data)
	return nil
}

type pgpScratchWriter struct {
	ctx     context.Context
	scratch *pgpScratch
}

func (w pgpScratchWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		if err := w.ctx.Err(); err != nil {
			return written, err
		}
		n := min(4096, len(p))
		if err := w.scratch.append(p[:n]); err != nil {
			return written, err
		}
		p = p[n:]
		written += n
	}
	return written, w.ctx.Err()
}
func pgpCompress(ctx context.Context, dst *pgpScratch, input []byte, algorithm pgpCompression, level int) error {
	var writer io.WriteCloser
	var err error
	out := pgpScratchWriter{ctx, dst}
	switch algorithm {
	case 1:
		writer, err = flate.NewWriter(out, level)
	case 2:
		writer, err = zlib.NewWriterLevel(out, level)
	default:
		return pgpError("Unsupported compression algorithm")
	}
	if err != nil {
		return pgpError("Compression error")
	}
	for len(input) > 0 {
		if err = ctx.Err(); err != nil {
			break
		}
		n := min(4096, len(input))
		if _, err = writer.Write(input[:n]); err != nil {
			break
		}
		input = input[n:]
	}
	closed := writer.Close()
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil || closed != nil {
		return pgpError("Compression error")
	}
	return nil
}
func pgpDecompress(ctx context.Context, dst *pgpScratch, input []byte, algorithm byte, source *pgpPacket) error {
	if algorithm == 0 {
		return pgpScratchWriter{ctx, dst}.writeAll(input)
	}
	var reader io.ReadCloser
	var err error
	compressed := pgpInflateInput{data: input, chunkEnd: len(input)}
	if source != nil && source.partial {
		compressed.wire = source.wire
		compressed.chunkEnd = -1
	}
	switch algorithm {
	case 1:
		reader = flate.NewReader(&compressed)
	case 2:
		reader, err = zlib.NewReader(&compressed)
	case 3:
		return pgpError("Unsupported compression algorithm")
	default:
		return pgpCorrupt()
	}
	if err != nil {
		return pgpCorrupt()
	}
	var scratch [4096]byte
	defer clear(scratch[:])
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		var n int
		n, err = reader.Read(scratch[:])
		if n > 0 {
			if added := dst.append(scratch[:n]); added != nil {
				err = added
				break
			}
		}
		if err != nil {
			break
		}
	}
	closed := reader.Close()
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != io.EOF || closed != nil || compressed.fetched != len(input) {
		return pgpCorrupt()
	}
	return nil
}
func (w pgpScratchWriter) writeAll(p []byte) error { _, err := w.Write(p); return err }

// REL_18_6 decompress_read borrows at most8192 input bytes at once, clipped
// to the current packet chunk. Suffix bytes already borrowed at STREAM_END
// are ignored; any still in the packet source are corruption. Track fetched
// bytes independently of inflater consumption instead of trusting Go buffering.
type pgpInflateInput struct {
	data                   []byte
	pos, fetched, chunkEnd int
	wire                   pgpInput
}

func (r *pgpInflateInput) refill() error {
	if r.pos == len(r.data) {
		return io.EOF
	}
	if r.pos < r.fetched {
		return nil
	}
	for r.pos >= r.chunkEnd {
		if r.wire.len() == 0 {
			return pgpCorrupt()
		}
		n, _, width, err := pgpReadLength(r.wire)
		if err != nil || n > r.wire.len()-width {
			return pgpCorrupt()
		}
		r.chunkEnd += n
		r.wire = r.wire.sub(width+n, r.wire.len())
	}
	r.fetched = r.pos + min(8192, min(len(r.data)-r.pos, r.chunkEnd-r.pos))
	return nil
}
func (r *pgpInflateInput) ReadByte() (byte, error) {
	if err := r.refill(); err != nil {
		return 0, err
	}
	b := r.data[r.pos]
	r.pos++
	return b, nil
}
func (r *pgpInflateInput) Read(dst []byte) (int, error) {
	if len(dst) == 0 {
		return 0, nil
	}
	if err := r.refill(); err != nil {
		return 0, err
	}
	n := copy(dst, r.data[r.pos:r.fetched])
	r.pos += n
	return n, nil
}
