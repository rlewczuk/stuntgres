package sqltype

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"hash/crc32"
)

var binaryCRCCastagnoli = crc32.MakeTable(crc32.Castagnoli)

// Read raw octets with the immutable standard-library CRC tables, avoiding
// the []byte adaptation required by crc32.Update. State is per evaluation.
func binaryChecksum(ctx context.Context, name string, result *Descriptor, raw string) (Value, error) {
	table := crc32.IEEETable
	if name == "crc32c" {
		table = binaryCRCCastagnoli
	}
	checksum := ^uint32(0)
	for start := 0; start < len(raw); {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		end := start + min(4096, len(raw)-start)
		for ; start < end; start++ {
			checksum = table[byte(checksum)^raw[start]] ^ (checksum >> 8)
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	// Preserve the complete unsigned uint32 range in PostgreSQL BIGINT.
	return Integer(result, int64(^checksum))
}

func binaryDigest(ctx context.Context, name string, result *Descriptor, raw string) (Value, error) {
	var digest hash.Hash
	switch name {
	case "md5":
		digest = md5.New()
	case "sha224":
		digest = sha256.New224()
	case "sha256":
		digest = sha256.New()
	case "sha384":
		digest = sha512.New384()
	case "sha512":
		digest = sha512.New()
	default:
		return Value{}, fmt.Errorf("invalid binary digest")
	}
	// Value owns an immutable string; bounded scratch safely adapts it to the
	// standard hash APIs without a payload-sized copy or unsafe mutable alias.
	var scratch [4096]byte
	for start := 0; start < len(raw); {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		n := min(len(scratch), len(raw)-start)
		copy(scratch[:n], raw[start:start+n])
		if _, err := digest.Write(scratch[:n]); err != nil {
			return Value{}, err
		}
		start += n
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	var sum [sha512.Size]byte
	bytes := digest.Sum(sum[:0])
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if name == "md5" {
		var encoded [md5.Size * 2]byte
		hex.Encode(encoded[:], bytes)
		return String(result, string(encoded[:])), nil
	}
	return Binary(result, string(bytes))
}
