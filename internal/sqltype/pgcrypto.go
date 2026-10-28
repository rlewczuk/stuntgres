package sqltype

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"fmt"
	"hash"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

// PgcryptoPlan records only the admitted SQL signature, never a frame's algorithm.
type PgcryptoPlan struct {
	name      string
	result    Usage
	input     ID
	count     int
	arguments [4]ID
}

func BindPgcrypto(name string, result Usage, args ...Usage) (PgcryptoPlan, error) {
	count, resultID := 2, Bytea
	switch name {
	case "digest":
	case "hmac":
		count = 3
	case "encrypt", "decrypt":
		count = 3
	case "encrypt_iv", "decrypt_iv":
		count = 4
	case "crypt":
		resultID = Text
	case "gen_salt":
		count, resultID = len(args), Text
		if count != 1 && count != 2 {
			return PgcryptoPlan{}, fmt.Errorf("invalid crypto signature")
		}
	case "gen_random_bytes":
		count = 1
	case "gen_random_uuid":
		count, resultID = 0, UUID
	case "fips_mode":
		count, resultID = 0, Bool
	default:
		return PgcryptoPlan{}, fmt.Errorf("invalid crypto function")
	}
	if len(args) != count || result.Type == nil || result.Type.ID() != resultID {
		return PgcryptoPlan{}, fmt.Errorf("invalid crypto signature")
	}
	var input ID
	if count != 0 {
		if args[0].Type == nil {
			return PgcryptoPlan{}, fmt.Errorf("invalid crypto input")
		}
		input = args[0].Type.ID()
		rawCipher := name == "encrypt" || name == "decrypt" || name == "encrypt_iv" || name == "decrypt_iv"
		if name == "gen_random_bytes" {
			if input != Int4 {
				return PgcryptoPlan{}, fmt.Errorf("invalid crypto input")
			}
		} else if rawCipher && input != Bytea || !rawCipher && input != Text && (input != Bytea || name != "digest" && name != "hmac") {
			return PgcryptoPlan{}, fmt.Errorf("invalid crypto input")
		}
	}
	plan := PgcryptoPlan{name: name, result: result, input: input, count: count}
	for i, arg := range args {
		want := input
		if i == count-1 && name != "gen_random_bytes" {
			want = Text
		}
		if name == "gen_salt" && i == 1 {
			want = Int4
		}
		if arg.Type == nil || arg.Type.ID() != want {
			return PgcryptoPlan{}, fmt.Errorf("invalid crypto argument")
		}
		plan.arguments[i] = want
	}
	return plan, nil
}

func (p PgcryptoPlan) Name() string { return p.name }

func cryptoAlgorithm(raw string) string {
	end := min(len(raw), 63)
	for end < len(raw) && end > 0 && !utf8.RuneStart(raw[end]) {
		end--
	}
	upper := false
	for i := range end {
		if raw[i] >= 'A' && raw[i] <= 'Z' {
			upper = true
			break
		}
	}
	if !upper {
		return raw[:end]
	}
	var folded [63]byte
	for i := range end {
		c := raw[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		folded[i] = c
	}
	return string(folded[:end])
}

func cryptoHash(name string) (func() hash.Hash, int) {
	switch name {
	case "md5":
		return md5.New, md5.BlockSize
	case "sha1", "sha-1":
		return sha1.New, sha1.BlockSize
	case "sha224", "sha-224":
		return sha256.New224, sha256.BlockSize
	case "sha256", "sha-256", "sha2-256":
		return sha256.New, sha256.BlockSize
	case "sha384", "sha-384":
		return sha512.New384, sha512.BlockSize
	case "sha512", "sha-512":
		return sha512.New, sha512.BlockSize
	}
	return nil, 0
}

func (p PgcryptoPlan) Evaluate(ctx context.Context, notice func(sqlstate.Error), values ...Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	null, err := p.validateValues(values)
	if err != nil {
		return Value{}, err
	}
	if null {
		return Null(p.result.Type), nil
	}
	count := p.count
	if p.name == "crypt" {
		password, err := values[0].Text()
		if err != nil {
			return Value{}, err
		}
		salt, err := values[1].Text()
		if err != nil {
			return Value{}, err
		}
		hash, err := pgcryptoCrypt(ctx, notice, password, salt)
		if err != nil {
			return Value{}, err
		}
		return String(p.result.Type, hash), nil
	}
	switch p.name {
	case "gen_salt", "gen_random_bytes", "gen_random_uuid", "fips_mode":
		return Value{}, fmt.Errorf("%s requires an execution provider", p.name)
	}
	algorithm, err := values[count-1].Text()
	if err != nil {
		return Value{}, err
	}
	name := cryptoAlgorithm(algorithm)
	if p.name == "encrypt" || p.name == "decrypt" || p.name == "encrypt_iv" || p.name == "decrypt_iv" {
		data, err := values[0].Binary()
		if err != nil {
			return Value{}, err
		}
		key, err := values[1].Binary()
		if err != nil {
			return Value{}, err
		}
		var iv string
		if count == 4 {
			iv, err = values[2].Binary()
			if err != nil {
				return Value{}, err
			}
		}
		output, err := pgcryptoRaw(ctx, p.name, data, key, iv, name)
		if err != nil {
			return Value{}, err
		}
		return Binary(p.result.Type, output)
	}
	newHash, blockSize := cryptoHash(name)
	if newHash == nil {
		return Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: fmt.Sprintf("Cannot use \"%s\": No such hash algorithm", name)}
	}
	raw := func(v Value) (string, error) {
		if p.input == Bytea {
			return v.Binary()
		}
		return v.Text()
	}
	data, err := raw(values[0])
	if err != nil {
		return Value{}, err
	}
	var digest hash.Hash
	var scratch [4096]byte
	if p.name == "hmac" {
		key, err := raw(values[1])
		if err != nil {
			return Value{}, err
		}
		var effective [sha512.BlockSize]byte
		var size int
		if len(key) > blockSize {
			digest = newHash()
			if err := writeHashString(ctx, digest, key, &scratch, 0); err != nil {
				return Value{}, err
			}
			size = len(digest.Sum(effective[:0]))
		} else {
			size = copy(effective[:], key)
		}
		digest = hmac.New(newHash, effective[:size])
	} else {
		digest = newHash()
	}
	if err := writeHashString(ctx, digest, data, &scratch, 0); err != nil {
		return Value{}, err
	}
	var sum [sha512.Size]byte
	bytes := digest.Sum(sum[:0])
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Binary(p.result.Type, string(bytes))
}

func (p PgcryptoPlan) validateValues(values []Value) (bool, error) {
	if len(values) != p.count {
		return false, fmt.Errorf("invalid crypto values")
	}
	null := false
	for i, value := range values {
		if value.Type() == nil || value.Type().ID() != p.arguments[i] {
			return false, fmt.Errorf("invalid crypto value type")
		}
		null = null || value.IsNull()
	}
	return null, nil
}

// PrepareRandomBytes checks the SQL count before any allocation or entropy.
func (p PgcryptoPlan) PrepareRandomBytes(values ...Value) (int, bool, error) {
	if p.name != "gen_random_bytes" {
		return 0, false, fmt.Errorf("invalid random bytes plan")
	}
	null, err := p.validateValues(values)
	if err != nil || null {
		return 0, null, err
	}
	count, err := values[0].Integer()
	if err != nil {
		return 0, false, err
	}
	if count < 1 || count > 1024 {
		return 0, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "39000", Message: "Length not in range"}
	}
	return int(count), false, nil
}

func pgcryptoSaltAlgorithmEqual(raw, lower string) bool {
	if len(raw) != len(lower) {
		return false
	}
	for i := range raw {
		c := raw[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

func pgcryptoError(code, message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
