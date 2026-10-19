package sqltype

import (
	"context"
	"crypto/md5"
	"strings"
)

// pgcryptoMD5 uses bounded writeHashString scratch even for long passwords.
// Protocol and byte permutation: PostgreSQL REL_18_6 crypt-md5.c.
func pgcryptoMD5(ctx context.Context, password, setting string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if end := strings.IndexByte(password, 0); end >= 0 {
		password = password[:end]
	}
	salt := setting[3:]
	salt = salt[:min(8, len(salt))]
	if end := strings.IndexByte(salt, '$'); end >= 0 {
		salt = salt[:end]
	}
	alternate, digest := md5.New(), md5.New()
	var scratch [4096]byte
	for _, raw := range [...]string{password, salt, password} {
		if err := writeHashString(ctx, alternate, raw, &scratch); err != nil {
			return "", err
		}
	}
	var sum, other [md5.Size]byte
	alternate.Sum(other[:0])
	for _, raw := range [...]string{password, "$1$", salt} {
		if err := writeHashString(ctx, digest, raw, &scratch); err != nil {
			return "", err
		}
	}
	for remaining := len(password); remaining > 0; remaining -= min(remaining, md5.Size) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		digest.Write(other[:min(remaining, md5.Size)])
	}
	var octet [1]byte
	for bits := len(password); bits != 0; bits >>= 1 {
		octet[0] = 0
		if bits&1 == 0 {
			octet[0] = password[0]
		}
		digest.Write(octet[:])
	}
	digest.Sum(sum[:0])
	for round := range 1000 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		digest.Reset()
		if round&1 != 0 {
			if err := writeHashString(ctx, digest, password, &scratch); err != nil {
				return "", err
			}
		} else {
			digest.Write(sum[:])
		}
		if round%3 != 0 {
			if err := writeHashString(ctx, digest, salt, &scratch); err != nil {
				return "", err
			}
		}
		if round%7 != 0 {
			if err := writeHashString(ctx, digest, password, &scratch); err != nil {
				return "", err
			}
		}
		if round&1 != 0 {
			digest.Write(sum[:])
		} else if err := writeHashString(ctx, digest, password, &scratch); err != nil {
			return "", err
		}
		digest.Sum(sum[:0])
	}
	var result [34]byte
	copy(result[:], "$1$")
	copy(result[3:], salt)
	position := 3 + len(salt)
	result[position] = '$'
	position++
	for _, triple := range [...][3]int{{0, 6, 12}, {1, 7, 13}, {2, 8, 14}, {3, 9, 15}, {4, 10, 5}} {
		value := uint32(sum[triple[0]])<<16 | uint32(sum[triple[1]])<<8 | uint32(sum[triple[2]])
		pgcryptoCryptEncode(result[position:position+4], value)
		position += 4
	}
	pgcryptoCryptEncode(result[position:position+2], uint32(sum[11]))
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(result[:position+2]), nil
}
