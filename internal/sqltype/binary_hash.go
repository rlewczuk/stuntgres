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

	"stuntgres/internal/workadmission"
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
	var scratch [4096]byte
	if err := writeHashString(ctx, digest, raw, &scratch, 0); err != nil {
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

// Adapt immutable octets with bounded scratch shared by builtin and installed hashes.
// class is invocation-local: only SHA-password derivation selects admission;
// unrelated digest, HMAC and MD5 work passes zero and retains cancellation only.
func writeHashString(ctx context.Context, digest hash.Hash, raw string, scratch *[4096]byte, class workadmission.Class) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for start := 0; start < len(raw); {
		if err := ctx.Err(); err != nil {
			return err
		}
		if class == workadmission.WorkPasswordSHA {
			if err := workadmission.Admit(ctx, class); err != nil {
				return err
			}
		}
		n := min(len(scratch), len(raw)-start)
		copy(scratch[:n], raw[start:start+n])
		if _, err := digest.Write(scratch[:n]); err != nil {
			return err
		}
		start += n
	}
	return ctx.Err()
}
