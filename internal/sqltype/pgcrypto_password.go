package sqltype

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/sqlstate"
)

const pgcryptoCryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

type pgcryptoSaltKind uint8

const (
	pgcryptoSaltDES pgcryptoSaltKind = iota + 1
	pgcryptoSaltXDES
	pgcryptoSaltMD5
	pgcryptoSaltBcrypt
	pgcryptoSaltSHA256
	pgcryptoSaltSHA512
)

// PgcryptoSaltParameters is a validated, execution-local generator request.
// Neither it nor entropy is retained on the signature-only PgcryptoPlan.
type PgcryptoSaltParameters struct {
	kind   pgcryptoSaltKind
	rounds uint32
}

func (p PgcryptoSaltParameters) EntropySize() int {
	switch p.kind {
	case pgcryptoSaltDES:
		return 2
	case pgcryptoSaltXDES:
		return 3
	case pgcryptoSaltMD5:
		return 6
	case pgcryptoSaltBcrypt, pgcryptoSaltSHA256, pgcryptoSaltSHA512:
		return 16
	}
	return 0
}

// PrepareSalt validates signed SQL counts before conversion or entropy access.
func (p PgcryptoPlan) PrepareSalt(values ...Value) (PgcryptoSaltParameters, bool, error) {
	var parameters PgcryptoSaltParameters
	if p.name != "gen_salt" {
		return parameters, false, fmt.Errorf("invalid salt plan")
	}
	null, err := p.validateValues(values)
	if err != nil || null {
		return parameters, null, err
	}
	name, err := values[0].Text()
	if err != nil {
		return parameters, false, err
	}
	var defaultRounds int64
	switch {
	case pgcryptoSaltAlgorithmEqual(name, "des"):
		parameters.kind, defaultRounds = pgcryptoSaltDES, 25
	case pgcryptoSaltAlgorithmEqual(name, "xdes"):
		parameters.kind, defaultRounds = pgcryptoSaltXDES, 725
	case pgcryptoSaltAlgorithmEqual(name, "md5"):
		parameters.kind, defaultRounds = pgcryptoSaltMD5, 1000
	case pgcryptoSaltAlgorithmEqual(name, "bf"):
		parameters.kind, defaultRounds = pgcryptoSaltBcrypt, 6
	case pgcryptoSaltAlgorithmEqual(name, "sha256crypt"):
		parameters.kind, defaultRounds = pgcryptoSaltSHA256, 5000
	case pgcryptoSaltAlgorithmEqual(name, "sha512crypt"):
		parameters.kind, defaultRounds = pgcryptoSaltSHA512, 5000
	default:
		return PgcryptoSaltParameters{}, false, pgcryptoError("22023", "gen_salt: Unknown salt algorithm")
	}
	rounds := defaultRounds
	if len(values) == 2 {
		rounds, err = values[1].Integer()
		if err != nil {
			return PgcryptoSaltParameters{}, false, err
		}
		if rounds == 0 {
			rounds = defaultRounds
		}
	}
	valid := rounds == defaultRounds
	switch parameters.kind {
	case pgcryptoSaltXDES:
		valid = rounds >= 1 && rounds <= 0xffffff && rounds&1 != 0
	case pgcryptoSaltBcrypt:
		valid = rounds >= 4 && rounds <= 31
	case pgcryptoSaltSHA256, pgcryptoSaltSHA512:
		valid = rounds >= 1000 && rounds <= 999999999
	}
	if !valid {
		return PgcryptoSaltParameters{}, false, pgcryptoError("22023", "gen_salt: Incorrect number of rounds")
	}
	parameters.rounds = uint32(rounds)
	return parameters, false, nil
}

func pgcryptoCryptEncode(dst []byte, value uint32) {
	for i := range dst {
		dst[i] = pgcryptoCryptAlphabet[value&63]
		value >>= 6
	}
}

// PgcryptoSalt consumes only parameters returned successfully by PrepareSalt.
func PgcryptoSalt(parameters PgcryptoSaltParameters, entropy [16]byte) string {
	var result [36]byte
	switch parameters.kind {
	case pgcryptoSaltDES:
		result[0], result[1] = pgcryptoCryptAlphabet[entropy[0]&63], pgcryptoCryptAlphabet[entropy[1]&63]
		return string(result[:2])
	case pgcryptoSaltXDES:
		result[0] = '_'
		pgcryptoCryptEncode(result[1:5], parameters.rounds)
		pgcryptoCryptEncode(result[5:9], uint32(entropy[0])|uint32(entropy[1])<<8|uint32(entropy[2])<<16)
		return string(result[:9])
	case pgcryptoSaltMD5:
		copy(result[:], "$1$")
		for i := range 2 {
			offset := i * 3
			pgcryptoCryptEncode(result[3+i*4:7+i*4], uint32(entropy[offset])|uint32(entropy[offset+1])<<8|uint32(entropy[offset+2])<<16)
		}
		return string(result[:11])
	case pgcryptoSaltBcrypt:
		cost := uint8(parameters.rounds)
		copy(result[:], "$2a$00$")
		result[4], result[5] = '0'+cost/10, '0'+cost%10
		pgcryptoBcryptEncode(result[7:29], entropy[:])
		return string(result[:29])
	case pgcryptoSaltSHA256, pgcryptoSaltSHA512:
		id := byte('5')
		if parameters.kind == pgcryptoSaltSHA512 {
			id = '6'
		}
		result[0], result[1], result[2] = '$', id, '$'
		copy(result[3:], "rounds=")
		out := strconv.AppendUint(result[:10], uint64(parameters.rounds), 10)
		out = append(out, '$')
		for _, b := range entropy {
			out = append(out, pgcryptoCryptAlphabet[b&63])
		}
		return string(out)
	}
	panic("unvalidated pgcrypto salt parameters")
}

func pgcryptoCrypt(ctx context.Context, notice func(sqlstate.Error), password, setting string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	switch {
	case strings.HasPrefix(setting, "$2a$"), strings.HasPrefix(setting, "$2x$"):
		return pgcryptoBcrypt(ctx, password, setting)
	case strings.HasPrefix(setting, "$2$"):
		return "", pgcryptoError("39000", "crypt(3) returned NULL")
	case strings.HasPrefix(setting, "$1$"):
		return pgcryptoMD5(ctx, password, setting)
	case strings.HasPrefix(setting, "$5$"), strings.HasPrefix(setting, "$6$"):
		return pgcryptoSHA(ctx, notice, password, setting)
	default:
		return pgcryptoDES(ctx, password, setting)
	}
}
