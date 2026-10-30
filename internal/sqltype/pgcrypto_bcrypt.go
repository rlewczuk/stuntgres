package sqltype

import (
	"context"
	"encoding/binary"
	"strings"

	"stuntgres/internal/util"
	"stuntgres/internal/workadmission"
)

// PostgreSQL's bcrypt protocol derives from Solar Designer's public-domain
// crypt-blowfish.c (1998–2002), REL_18_0. This implementation deliberately keeps
// PostgreSQL's 2a unsigned expansion and 2x historical sign-extension behavior,
// not the later Openwall 2a collision countermeasure or modern 2b/2y dispatch.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/contrib/pgcrypto/crypt-blowfish.c
const pgcryptoBcryptAlphabet = "./ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

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

// pgcryptoBcrypt consumes one admission quantum for each actual bcrypt round.
// All state/scratch is fixed size. Cost31 is representable; cancellation, not
// an arbitrary lower safety ceiling, bounds abandoned expensive operations.
func pgcryptoBcrypt(ctx context.Context, password, salt string) (string, error) {
	if err := util.ExecutionCheckpoint(ctx, "crypto.bcrypt"); err != nil {
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
	state := pgcryptoBlowfishInitial
	state.expand(key[:], &saltWords)
	for round, rounds := uint64(0), uint64(1)<<cost; round < rounds; round++ {
		if err := workadmission.Admit(ctx, workadmission.WorkPasswordBcrypt); err != nil {
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
