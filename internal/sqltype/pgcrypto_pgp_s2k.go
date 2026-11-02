package sqltype

import (
	"context"
	"crypto/sha1"
)

func pgpS2KCount(encoded byte) int { return (16 + int(encoded&15)) << ((encoded >> 4) + 6) }
func pgpS2KEncode(count int, random byte) byte {
	if count < 0 {
		return 96 + (random & 31)
	}
	for n := range 256 {
		if pgpS2KCount(byte(n)) >= count {
			return byte(n)
		}
	}
	return 255
}

// Derive precisely the AES128 key. Password strings remain caller-owned; every
// mutable password-containing buffer and digest output is cleared on exit.
func pgpS2KKey(ctx context.Context, password string, salt []byte, encoded byte) ([16]byte, error) {
	var key [16]byte
	if len(salt) != 8 || len(password) == 0 {
		return key, pgpArgument()
	}
	if err := ctx.Err(); err != nil {
		return key, err
	}
	count := max(pgpS2KCount(encoded), len(password)+8)
	h := sha1.New()
	var scratch [4096]byte
	defer clear(scratch[:])
	unit := len(password) + 8
	if unit <= len(scratch) {
		copy(scratch[:8], salt)
		copy(scratch[8:unit], password)
		filled := unit
		limit := len(scratch) / unit * unit
		for filled < limit {
			filled += copy(scratch[filled:limit], scratch[:filled])
		}
		for count > 0 {
			if err := ctx.Err(); err != nil {
				return key, err
			}
			n := min(count, limit)
			h.Write(scratch[:n])
			count -= n
		}
	} else {
		// At least one complete salt/password unit is hashed, even when the
		// explicit iteration count is smaller than that unit.
		for count > 0 {
			if err := ctx.Err(); err != nil {
				return key, err
			}
			n := min(count, 8)
			h.Write(salt[:n])
			count -= n
			for pos := 0; pos < len(password) && count > 0; {
				if err := ctx.Err(); err != nil {
					return key, err
				}
				n = min(count, min(len(scratch), len(password)-pos))
				copy(scratch[:n], password[pos:pos+n])
				h.Write(scratch[:n])
				pos += n
				count -= n
			}
		}
	}
	var digest [sha1.Size]byte
	h.Sum(digest[:0])
	copy(key[:], digest[:16])
	clear(digest[:])
	h.Reset()
	if err := ctx.Err(); err != nil {
		clear(key[:])
		return key, err
	}
	return key, nil
}

func pgpSHA1Sum(ctx context.Context, data []byte) ([sha1.Size]byte, error) {
	var sum [sha1.Size]byte
	h := sha1.New()
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		n := min(4096, len(data))
		h.Write(data[:n])
		data = data[n:]
	}
	h.Sum(sum[:0])
	h.Reset()
	return sum, ctx.Err()
}
