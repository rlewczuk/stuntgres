package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// hstoreCursor walks the virtual key/value byte stream without materializing it.
// Empty spans (including NULL values) still reach bounded cancellation checks.
type hstoreCursor struct {
	pairs []hstorePair
	entry int
}

func (c *hstoreCursor) next(ctx context.Context) (string, error) {
	for c.entry/2 < len(c.pairs) {
		if c.entry&255 == 0 {
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		p := c.pairs[c.entry/2]
		span := p.key
		if c.entry&1 != 0 {
			span = p.value
		}
		c.entry++
		if len(span) != 0 {
			return span, nil
		}
	}
	return "", ctx.Err()
}

func compareHstore(ctx context.Context, a, b *hstorePayload) (int, error) {
	if len(a.pairs) == 0 || len(b.pairs) == 0 {
		return compareHstoreSize(len(a.pairs), len(b.pairs)), ctx.Err()
	}
	left, right := hstoreCursor{pairs: a.pairs}, hstoreCursor{pairs: b.pairs}
	var x, y string
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		var err error
		if len(x) == 0 {
			x, err = left.next(ctx)
			if err != nil {
				return 0, err
			}
		}
		if len(y) == 0 {
			y, err = right.next(ctx)
			if err != nil {
				return 0, err
			}
		}
		if len(x) == 0 || len(y) == 0 {
			if n := compareHstoreSize(len(x), len(y)); n != 0 {
				return n, nil
			}
			break
		}
		n := min(len(x), len(y), 1024)
		if sign := strings.Compare(x[:n], y[:n]); sign != 0 {
			return sign, nil
		}
		x, y = x[n:], y[n:]
	}
	if n := compareHstoreSize(len(a.pairs), len(b.pairs)); n != 0 {
		return n, nil
	}
	var leftEnd, rightEnd int
	for i, p := range a.pairs {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		q := b.pairs[i]
		leftEnd += len(p.key)
		rightEnd += len(q.key)
		if n := compareHstoreSize(leftEnd, rightEnd); n != 0 {
			return n, nil
		}
		leftEnd += len(p.value)
		rightEnd += len(q.value)
		if n := compareHstoreSize(leftEnd, rightEnd); n != 0 {
			return n, nil
		}
		if p.null != q.null {
			if p.null {
				return 1, nil
			}
			return -1, nil
		}
	}
	return 0, ctx.Err()
}

func compareHstoreSize(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// HstoreHash streams the canonical little-endian PostgreSQL hstore image into
// hash_any_extended. It excludes the varlena header and preserves count, entry
// boundaries and NULL flags. Seed zero's low 32 bits are hstore_hash's result.
// SQL NULL framing and installed routine authority belong to the caller.
func HstoreHash(ctx context.Context, v Value, seed uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if v.typ == nil || !v.typ.BaseType().IsHstore() || v.null || !v.validHstore() {
		return 0, fmt.Errorf("invalid hstore hash payload")
	}
	pairs := v.hstore.pairs
	length := 4 + 8*len(pairs)
	for i, p := range pairs {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		length += len(p.key) + len(p.value)
	}
	state := newPartitionHashState(length, seed)
	putWord := func(word uint32) {
		state.put(byte(word))
		state.put(byte(word >> 8))
		state.put(byte(word >> 16))
		state.put(byte(word >> 24))
	}
	putWord(uint32(len(pairs)) | 0x80000000)
	var end uint32
	for i, p := range pairs {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		end += uint32(len(p.key))
		key := end
		if i == 0 {
			key |= 0x80000000
		}
		putWord(key)
		end += uint32(len(p.value))
		value := end
		if p.null {
			value |= 0x40000000
		}
		putWord(value)
	}
	for i, p := range pairs {
		if i&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		for _, span := range [2]string{p.key, p.value} {
			for i := range len(span) {
				if i&1023 == 0 {
					if err := ctx.Err(); err != nil {
						return 0, err
					}
				}
				state.put(span[i])
			}
		}
	}
	return state.finish(), ctx.Err()
}
