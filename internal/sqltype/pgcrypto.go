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
	name   string
	result Usage
	input  ID
}

func BindPgcrypto(name string, result Usage, args ...Usage) (PgcryptoPlan, error) {
	count := 2
	if name == "hmac" {
		count = 3
	} else if name != "digest" {
		return PgcryptoPlan{}, fmt.Errorf("invalid crypto function")
	}
	if len(args) != count || result.Type == nil || result.Type.ID() != Bytea || args[0].Type == nil {
		return PgcryptoPlan{}, fmt.Errorf("invalid crypto signature")
	}
	input := args[0].Type.ID()
	if input != Text && input != Bytea {
		return PgcryptoPlan{}, fmt.Errorf("invalid crypto input")
	}
	for i, arg := range args {
		want := input
		if i == count-1 {
			want = Text
		}
		if arg.Type == nil || arg.Type.ID() != want {
			return PgcryptoPlan{}, fmt.Errorf("invalid crypto argument")
		}
	}
	return PgcryptoPlan{name: name, result: result, input: input}, nil
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

func (p PgcryptoPlan) Evaluate(ctx context.Context, values ...Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	count := 2
	if p.name == "hmac" {
		count = 3
	}
	if len(values) != count {
		return Value{}, fmt.Errorf("invalid crypto values")
	}
	for i, value := range values {
		want := p.input
		if i == count-1 {
			want = Text
		}
		if value.Type() == nil || value.Type().ID() != want {
			return Value{}, fmt.Errorf("invalid crypto value type")
		}
	}
	for _, value := range values {
		if value.IsNull() {
			return Null(p.result.Type), nil
		}
	}
	algorithm, err := values[count-1].Text()
	if err != nil {
		return Value{}, err
	}
	name := cryptoAlgorithm(algorithm)
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
	if p.name == "hmac" {
		key, err := raw(values[1])
		if err != nil {
			return Value{}, err
		}
		var effective [sha512.BlockSize]byte
		var size int
		if len(key) > blockSize {
			digest = newHash()
			if err := writeHashString(ctx, digest, key); err != nil {
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
	if err := writeHashString(ctx, digest, data); err != nil {
		return Value{}, err
	}
	var sum [sha512.Size]byte
	bytes := digest.Sum(sum[:0])
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Binary(p.result.Type, string(bytes))
}
