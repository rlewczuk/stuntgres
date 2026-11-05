package sqltype

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"hash"
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
func pgpS2KHash(digest pgpDigest) hash.Hash {
	switch digest {
	case 1:
		return md5.New()
	case 2:
		return sha1.New()
	case 8:
		return sha256.New()
	}
	return nil
}

// Derive into caller-owned key storage. Every digest stream gets an increasing
// zero prefix; mutable password scratch and digest output are erased on exit.
func pgpS2KKey(ctx context.Context, key []byte, password string, salt []byte, mode int, digest pgpDigest, encoded byte) (err error) {
	defer func() {
		if err != nil {
			clear(key)
		}
	}()
	if len(password) == 0 || len(key) == 0 || len(key) > 32 {
		return pgpArgument()
	}
	if mode != 0 && mode != 1 && mode != 3 {
		return pgpError("Bad S2K mode")
	}
	if mode != 0 && len(salt) != 8 {
		return pgpCorrupt()
	}
	h := pgpS2KHash(digest)
	if h == nil {
		switch digest {
		case 3, 9, 10:
			return pgpCapability()
		}
		return pgpCorrupt()
	}
	defer h.Reset()
	var scratch [4096]byte
	var sum [sha256.Size]byte
	defer clear(scratch[:])
	defer clear(sum[:])
	unit := len(password)
	if mode != 0 {
		if unit > int(^uint(0)>>1)-8 {
			return pgpArgument()
		}
		unit += 8
	}
	count := unit
	if mode == 3 {
		count = max(count, pgpS2KCount(encoded))
	}
	for written, stream := 0, 0; written < len(key); stream++ {
		h.Reset()
		h.Write(scratch[:stream]) // Zero prefix, before filling password scratch.
		if unit <= len(scratch) {
			start := 0
			if mode != 0 {
				start = copy(scratch[:], salt)
			}
			copy(scratch[start:unit], password)
			limit := len(scratch) / unit * unit
			for filled := unit; filled < limit; {
				filled += copy(scratch[filled:limit], scratch[:filled])
			}
			for left := count; left > 0; {
				if err := ctx.Err(); err != nil {
					return err
				}
				n := min(left, limit)
				h.Write(scratch[:n])
				left -= n
			}
		} else {
			for left := count; left > 0; {
				if err := ctx.Err(); err != nil {
					return err
				}
				if mode != 0 {
					n := min(left, 8)
					h.Write(salt[:n])
					left -= n
				}
				for pos := 0; pos < len(password) && left > 0; {
					if err := ctx.Err(); err != nil {
						return err
					}
					n := min(left, min(len(scratch), len(password)-pos))
					copy(scratch[:n], password[pos:pos+n])
					h.Write(scratch[:n])
					pos += n
					left -= n
				}
			}
		}
		h.Sum(sum[:0])
		written += copy(key[written:], sum[:h.Size()])
		clear(scratch[:])
		clear(sum[:])
	}
	return ctx.Err()
}

func pgpSHA1Sum(ctx context.Context, data []byte) ([sha1.Size]byte, error) {
	var sum [sha1.Size]byte
	h := sha1.New()
	defer h.Reset()
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return sum, err
		}
		n := min(4096, len(data))
		h.Write(data[:n])
		data = data[n:]
	}
	h.Sum(sum[:0])
	if err := ctx.Err(); err != nil {
		clear(sum[:])
		return sum, err
	}
	return sum, nil
}
