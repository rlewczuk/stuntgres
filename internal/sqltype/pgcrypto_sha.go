package sqltype

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"strconv"

	"stuntgres/internal/sqlstate"
)

// SHA-crypt steps1-22 follow the public-domain Drepper reference and PostgreSQL
// REL_18_6 crypt-sha.c. Only per-call standard-library hash state is retained.
func pgcryptoSHA(ctx context.Context, notice func(sqlstate.Error), password, setting string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p, err := pgcryptoSHAParse(setting, notice)
	if err != nil {
		return "", err
	}
	var a, b hash.Hash
	if p.id == '5' {
		a, b = sha256.New(), sha256.New()
	} else {
		a, b = sha512.New(), sha512.New()
	}
	var scratch [4096]byte
	var current, sequenceP, sequenceS [64]byte
	size := a.Size()
	// A starts with password and decoded salt; B uses the raw salt prefix.
	if err := writeHashString(ctx, a, password, &scratch); err != nil {
		return "", err
	}
	a.Write(p.salt[:p.size])
	if err := writeHashString(ctx, b, password, &scratch); err != nil {
		return "", err
	}
	if err := writeHashString(ctx, b, p.raw, &scratch); err != nil {
		return "", err
	}
	if err := writeHashString(ctx, b, password, &scratch); err != nil {
		return "", err
	}
	b.Sum(current[:0])
	if err := pgcryptoSHARepeat(ctx, a, current[:size], len(password)); err != nil {
		return "", err
	}
	for bits := len(password); bits != 0; bits >>= 1 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if bits&1 != 0 {
			a.Write(current[:size])
		} else if err := writeHashString(ctx, a, password, &scratch); err != nil {
			return "", err
		}
	}
	a.Sum(current[:0])
	// DP inherently hashes len(password)^2 bytes. Stream them with bounded
	// scratch and cancellation, never allocate a password-sized P sequence.
	b.Reset()
	for range len(password) {
		if err := writeHashString(ctx, b, password, &scratch); err != nil {
			return "", err
		}
	}
	b.Sum(sequenceP[:0])
	b.Reset()
	for range 16 + int(current[0]) {
		if err := writeHashString(ctx, b, p.raw, &scratch); err != nil {
			return "", err
		}
	}
	b.Sum(sequenceS[:0])
	for round := range p.rounds {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		b.Reset()
		if round&1 != 0 {
			if err := pgcryptoSHARepeat(ctx, b, sequenceP[:size], len(password)); err != nil {
				return "", err
			}
		} else {
			b.Write(current[:size])
		}
		if round%3 != 0 {
			b.Write(sequenceS[:p.size])
		}
		if round%7 != 0 {
			if err := pgcryptoSHARepeat(ctx, b, sequenceP[:size], len(password)); err != nil {
				return "", err
			}
		}
		if round&1 != 0 {
			b.Write(current[:size])
		} else if err := pgcryptoSHARepeat(ctx, b, sequenceP[:size], len(password)); err != nil {
			return "", err
		}
		b.Sum(current[:0])
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var result [123]byte
	result[0], result[1], result[2] = '$', p.id, '$'
	out := result[:3]
	if p.custom {
		out = append(out, "rounds="...)
		out = strconv.AppendUint(out, uint64(p.rounds), 10)
		out = append(out, '$')
	}
	out = append(out, p.salt[:p.size]...)
	out = append(out, '$')
	if p.id == '5' {
		for _, group := range pgcryptoSHA256Permutation {
			out = pgcryptoSHAEncode(out, current[group[0]], current[group[1]], current[group[2]], 4)
		}
		out = pgcryptoSHAEncode(out, 0, current[31], current[30], 3)
	} else {
		for _, group := range pgcryptoSHA512Permutation {
			out = pgcryptoSHAEncode(out, current[group[0]], current[group[1]], current[group[2]], 4)
		}
		out = pgcryptoSHAEncode(out, 0, 0, current[63], 2)
	}
	return string(out), nil
}

// Stream a repeated digest, including its final partial block. No per-round
// allocation or materialized P string; cancellation bounds large input work.
func pgcryptoSHARepeat(ctx context.Context, h hash.Hash, digest []byte, count int) error {
	for count > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(count, len(digest))
		h.Write(digest[:n])
		count -= n
	}
	return ctx.Err()
}

func pgcryptoSHAEncode(out []byte, high, middle, low byte, count int) []byte {
	value := uint32(high)<<16 | uint32(middle)<<8 | uint32(low)
	for range count {
		out = append(out, pgcryptoCryptAlphabet[value&63])
		value >>= 6
	}
	return out
}

var pgcryptoSHA256Permutation = [...][3]uint8{
	{0, 10, 20}, {21, 1, 11}, {12, 22, 2}, {3, 13, 23}, {24, 4, 14},
	{15, 25, 5}, {6, 16, 26}, {27, 7, 17}, {18, 28, 8}, {9, 19, 29},
}
var pgcryptoSHA512Permutation = [...][3]uint8{
	{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4},
	{47, 5, 26}, {6, 27, 48}, {28, 49, 7}, {50, 8, 29}, {9, 30, 51},
	{31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13}, {56, 14, 35},
	{15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41},
}
