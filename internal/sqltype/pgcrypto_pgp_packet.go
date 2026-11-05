package sqltype

import "context"

type pgpTag uint8

const (
	pgpSKESK       pgpTag = 3
	pgpCompressed  pgpTag = 8
	pgpEncrypted   pgpTag = 9
	pgpLiteral     pgpTag = 11
	pgpSEIPD       pgpTag = 18
	pgpPartialSize        = 16384
	pgpMaxChunk           = 16 * 1024 * 1024
)

type pgpFraming uint8

const (
	pgpOuter pgpFraming = iota
	// The caller has decrypted the complete region and excluded any MDC.
	pgpDecryptedInner
	// EOF tolerance belongs only to recognized tag9 bodies/literal payloads.
	pgpNoMDCOuter
	pgpNoMDCInner
)

// A borrowed byte view permits framing over immutable BYTEA input and owned
// authenticated scratch without copying either whole representation.
type pgpInput struct {
	text string
	data []byte
}

func (s pgpInput) len() int {
	if s.data != nil {
		return len(s.data)
	}
	return len(s.text)
}
func (s pgpInput) at(i int) byte {
	if s.data != nil {
		return s.data[i]
	}
	return s.text[i]
}
func (s pgpInput) sub(a, b int) pgpInput {
	if s.data != nil {
		return pgpInput{data: s.data[a:b]}
	}
	return pgpInput{text: s.text[a:b]}
}
func (s pgpInput) copyTo(dst []byte) int {
	if s.data != nil {
		return copy(dst, s.data)
	}
	return copy(dst, s.text)
}

// For partial bodies, wire includes the checked length/chunk sequence. A second
// bounded walk copies directly into the invocation's final decrypt scratch;
// there is no allocation per chunk or concatenated intermediate packet body.
type pgpPacket struct {
	tag     pgpTag
	body    pgpInput
	wire    pgpInput
	size    int
	partial bool
	bodyEOF bool
}

func pgpReadLength(s pgpInput) (size int, partial bool, width int, err error) {
	if s.len() < 1 {
		return 0, false, 0, pgpCorrupt()
	}
	b := s.at(0)
	switch {
	case b < 192:
		return int(b), false, 1, nil
	case b < 224:
		if s.len() < 2 {
			return 0, false, 0, pgpCorrupt()
		}
		return (int(b)-192)*256 + int(s.at(1)) + 192, false, 2, nil
	case b == 255:
		if s.len() < 5 {
			return 0, false, 0, pgpCorrupt()
		}
		n := uint64(s.at(1))<<24 | uint64(s.at(2))<<16 | uint64(s.at(3))<<8 | uint64(s.at(4))
		// The release bound applies to each chunk, never its packet aggregate.
		if n > pgpMaxChunk {
			return 0, false, 0, pgpCorrupt()
		}
		return int(n), false, 5, nil
	default:
		n := uint64(1) << uint(b&31)
		if n > pgpMaxChunk {
			return 0, false, 0, pgpCorrupt()
		}
		return int(n), true, 1, nil
	}
}

func pgpReadPacket(ctx context.Context, s pgpInput, framing pgpFraming) (pgpPacket, pgpInput, error) {
	var p pgpPacket
	if err := ctx.Err(); err != nil {
		return p, pgpInput{}, err
	}
	if s.len() < 1 || s.at(0)&128 == 0 {
		return p, pgpInput{}, pgpCorrupt()
	}
	h := s.at(0)
	if h&64 == 0 {
		p.tag = pgpTag((h >> 2) & 15)
		kind := h & 3
		if kind == 3 {
			if framing != pgpDecryptedInner && framing != pgpNoMDCInner {
				return p, pgpInput{}, pgpCorrupt()
			}
			p.size = s.len() - 1
			p.body = s.sub(1, s.len())
			return p, pgpInput{}, nil
		}
		width := 1 << kind
		if s.len() < 1+width {
			return p, pgpInput{}, pgpCorrupt()
		}
		var size uint64
		for i := range width {
			size = size<<8 | uint64(s.at(1+i))
		}
		start := 1 + width
		if size > pgpMaxChunk {
			return p, pgpInput{}, pgpCorrupt()
		}
		p.size = int(size)
		if p.size > s.len()-start {
			if !pgpAllowsBodyEOF(framing, p.tag) {
				return p, pgpInput{}, pgpCorrupt()
			}
			p.size, p.bodyEOF = s.len()-start, true
		}
		p.body = s.sub(start, start+p.size)
		return p, s.sub(start+p.size, s.len()), nil
	}
	p.tag = pgpTag(h & 63)
	pos := 1
	for {
		if err := ctx.Err(); err != nil {
			return p, pgpInput{}, err
		}
		n, partial, width, err := pgpReadLength(s.sub(pos, s.len()))
		if err != nil {
			return p, pgpInput{}, err
		}
		pos += width
		if n > s.len()-pos {
			if !pgpAllowsBodyEOF(framing, p.tag) {
				return p, pgpInput{}, pgpCorrupt()
			}
			n, p.bodyEOF = s.len()-pos, true
		}
		// The sum cannot exceed the supplied input, so cannot overflow int.
		p.size += n
		if partial {
			p.partial = true
		}
		if !p.partial {
			p.body = s.sub(pos, pos+n)
		}
		pos += n
		if !partial || p.bodyEOF {
			if p.partial {
				p.wire = s.sub(1, pos)
			}
			return p, s.sub(pos, s.len()), nil
		}
	}
}

func pgpAllowsBodyEOF(framing pgpFraming, tag pgpTag) bool {
	return framing == pgpNoMDCOuter && tag == pgpEncrypted ||
		framing == pgpNoMDCInner && tag == pgpLiteral
}

func (p pgpPacket) copyTo(ctx context.Context, dst []byte) error {
	if len(dst) != p.size {
		return pgpCorrupt()
	}
	if !p.partial {
		for pos := 0; pos < p.size; {
			if err := ctx.Err(); err != nil {
				return err
			}
			n := min(4096, p.size-pos)
			p.body.sub(pos, pos+n).copyTo(dst[pos : pos+n])
			pos += n
		}
		return ctx.Err()
	}
	s := p.wire
	written := 0
	for s.len() > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, partial, width, err := pgpReadLength(s)
		if err != nil {
			return err
		}
		if n > s.len()-width {
			if !p.bodyEOF {
				return pgpCorrupt()
			}
			n = s.len() - width
		}
		if n > len(dst)-written {
			return pgpCorrupt()
		}
		for offset := 0; offset < n; {
			if err := ctx.Err(); err != nil {
				return err
			}
			count := min(4096, n-offset)
			s.sub(width+offset, width+offset+count).copyTo(dst[written+offset : written+offset+count])
			offset += count
		}
		written += n
		s = s.sub(width+n, s.len())
		if !partial {
			break
		}
	}
	if written != len(dst) || s.len() != 0 {
		return pgpCorrupt()
	}
	return ctx.Err()
}

func pgpLengthWidth(n int) int {
	if n < 192 {
		return 1
	}
	if n < 8384 {
		return 2
	}
	return 5
}
func pgpWriteLength(dst []byte, n int) int {
	if n < 192 {
		dst[0] = byte(n)
		return 1
	}
	if n < 8384 {
		v := n - 192
		dst[0], dst[1] = byte(v>>8)+192, byte(v)
		return 2
	}
	dst[0], dst[1], dst[2], dst[3], dst[4] = 255, byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	return 5
}
func pgpPacketSize(n int) int {
	if n < 0 {
		return -1
	}
	overhead := 1 + n/pgpPartialSize + pgpLengthWidth(n%pgpPartialSize)
	if n > int(^uint(0)>>1)-overhead {
		return -1
	}
	return n + overhead
}

// Writes one new-format packet into exactly sized owned output. visit receives
// only writable payload slices, never framing bytes, and is never retained.
func pgpWritePacket(ctx context.Context, dst []byte, tag pgpTag, n int, visit func([]byte) error) error {
	if len(dst) != pgpPacketSize(n) {
		return pgpCorrupt()
	}
	dst[0] = 192 | byte(tag)
	pos := 1
	for n >= pgpPartialSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		dst[pos] = 238
		pos++
		if err := visit(dst[pos : pos+pgpPartialSize]); err != nil {
			return err
		}
		pos += pgpPartialSize
		n -= pgpPartialSize
	}
	pos += pgpWriteLength(dst[pos:], n)
	if err := ctx.Err(); err != nil {
		return err
	}
	return visit(dst[pos : pos+n])
}
