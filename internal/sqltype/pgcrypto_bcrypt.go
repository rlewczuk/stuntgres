package sqltype

import (
	"context"
	"encoding/binary"
	"strings"
)

// PostgreSQL's bcrypt protocol derives from Solar Designer's public-domain
// crypt-blowfish.c (1998–2002), REL_18_0. This implementation deliberately keeps
// PostgreSQL's 2a unsigned expansion and 2x historical sign-extension behavior,
// not the later Openwall 2a collision countermeasure or modern 2b/2y dispatch.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/contrib/pgcrypto/crypt-blowfish.c
const pgcryptoBcryptAlphabet = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

type pgcryptoBcryptState struct {
	p [18]uint32
	s [4][256]uint32
}

func (s *pgcryptoBcryptState) f(x uint32) uint32 {
	return ((s.s[0][byte(x>>24)] + s.s[1][byte(x>>16)]) ^ s.s[2][byte(x>>8)]) + s.s[3][byte(x)]
}

func (s *pgcryptoBcryptState) encrypt(left, right uint32) (uint32, uint32) {
	left ^= s.p[0]
	for i := 1; i <= 16; i += 2 {
		right ^= s.f(left) ^ s.p[i]
		left ^= s.f(right) ^ s.p[i+1]
	}
	return right ^ s.p[17], left
}

// expand never allocates. Salt is mixed into every encrypted block only for
// the initial expansion; subsequent password/salt expansions pass nil.
func (s *pgcryptoBcryptState) expand(key []uint32, salt *[4]uint32) {
	for i := range s.p {
		s.p[i] ^= key[i%len(key)]
	}
	var left, right uint32
	position := 0
	for i := 0; i < len(s.p); i += 2 {
		if salt != nil {
			left ^= salt[position&3]
			right ^= salt[(position+1)&3]
			position += 2
		}
		left, right = s.encrypt(left, right)
		s.p[i], s.p[i+1] = left, right
	}
	for box := range s.s {
		for i := 0; i < len(s.s[box]); i += 2 {
			if salt != nil {
				left ^= salt[position&3]
				right ^= salt[(position+1)&3]
				position += 2
			}
			left, right = s.encrypt(left, right)
			s.s[box][i], s.s[box][i+1] = left, right
		}
	}
}

func pgcryptoBcryptKey(password string, signed bool) (key [18]uint32) {
	position := 0
	for i := range key {
		for range 4 {
			var octet byte
			if position < len(password) {
				octet = password[position]
			}
			if octet == 0 {
				position = 0
			} else {
				position++
			}
			word := uint32(octet)
			if signed {
				word = uint32(int32(int8(octet)))
			}
			key[i] = key[i]<<8 | word
		}
	}
	return key
}

func pgcryptoBcryptSalt(salt string) (cost uint8, words [4]uint32, err error) {
	invalid := func() (uint8, [4]uint32, error) { return 0, [4]uint32{}, pgcryptoError("22023", "invalid salt") }
	if len(salt) < 29 || salt[4] < '0' || salt[4] > '9' || salt[5] < '0' || salt[5] > '9' || salt[6] != '$' {
		return invalid()
	}
	cost = (salt[4]-'0')*10 + salt[5] - '0'
	if cost < 4 || cost > 31 {
		return invalid()
	}
	var decoded [16]byte
	var bits uint32
	available, out := uint(0), 0
	for i := 7; i < 29; i++ {
		index := strings.IndexByte(pgcryptoBcryptAlphabet, salt[i])
		if index < 0 {
			return invalid()
		}
		bits = bits<<6 | uint32(index)
		available += 6
		if available >= 8 {
			available -= 8
			decoded[out] = byte(bits >> available)
			out++
		}
	}
	for i := range words {
		words[i] = binary.BigEndian.Uint32(decoded[i*4:])
	}
	return cost, words, nil
}

// encode writes custom bcrypt base64 without padding or permissive CR/LF input.
func pgcryptoBcryptEncode(dst, src []byte) {
	var bits uint32
	available, out := uint(0), 0
	for _, octet := range src {
		bits = bits<<8 | uint32(octet)
		available += 8
		for available >= 6 {
			available -= 6
			dst[out] = pgcryptoBcryptAlphabet[(bits>>available)&63]
			out++
		}
	}
	if available != 0 {
		dst[out] = pgcryptoBcryptAlphabet[(bits<<(6-available))&63]
	}
}

// pgcryptoBcrypt is the live cancellation-admission frame used by public tests.
// All state/scratch is fixed size. Cost31 is representable; cancellation, not
// an arbitrary lower safety ceiling, bounds abandoned expensive operations.
func pgcryptoBcrypt(ctx context.Context, password, salt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !strings.HasPrefix(salt, "$2a$") && !strings.HasPrefix(salt, "$2x$") {
		if strings.HasPrefix(salt, "$2$") {
			return "", pgcryptoError("39000", "crypt(3) returned NULL")
		}
		return "", pgcryptoError("0A000", "crypt: unsupported salt algorithm")
	}
	cost, saltWords, err := pgcryptoBcryptSalt(salt)
	if err != nil {
		return "", err
	}
	key := pgcryptoBcryptKey(password, salt[2] == 'x')
	state := pgcryptoBcryptInitial
	state.expand(key[:], &saltWords)
	for round, rounds := uint64(0), uint64(1)<<cost; round < rounds; round++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		state.expand(key[:], nil)
		state.expand(saltWords[:], nil)
	}
	output := [6]uint32{0x4f727068, 0x65616e42, 0x65686f6c, 0x64657253, 0x63727944, 0x6f756274}
	for i := 0; i < len(output); i += 2 {
		for range 64 {
			output[i], output[i+1] = state.encrypt(output[i], output[i+1])
		}
	}
	var saltBytes [16]byte
	for i, word := range saltWords {
		binary.BigEndian.PutUint32(saltBytes[i*4:], word)
	}
	var checksum [24]byte
	for i, word := range output {
		binary.BigEndian.PutUint32(checksum[i*4:], word)
	}
	var result [60]byte
	copy(result[:7], salt[:7])
	pgcryptoBcryptEncode(result[7:29], saltBytes[:])
	pgcryptoBcryptEncode(result[29:], checksum[:23])
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return string(result[:]), nil
}
