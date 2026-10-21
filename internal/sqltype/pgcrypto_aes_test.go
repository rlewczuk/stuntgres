package sqltype

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func aesUnitHex(t *testing.T, raw string) string {
	t.Helper()
	b, err := hex.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPgcryptoAESVectors(t *testing.T) {
	// NIST SP800-38A F.2.1 first block; SQL tests cover all six full vectors.
	plain := aesUnitHex(t, "6bc1bee22e409f96e93d7e117393172a")
	cipher := aesUnitHex(t, "7649abac8119b246cee98e9b12e9197d")
	key := aesUnitHex(t, "2b7e151628aed2a6abf7158809cf4f3c")
	iv := aesUnitHex(t, "000102030405060708090a0b0c0d0e0f")
	for _, tc := range []struct{ fn, input, want string }{{"encrypt_iv", plain, cipher}, {"decrypt_iv", cipher, plain}} {
		got, err := pgcryptoRaw(context.Background(), tc.fn, tc.input, key, iv, "aes/pad:none")
		if err != nil || got != tc.want {
			t.Fatalf("%s got %x err=%v", tc.fn, got, err)
		}
	}
}

func TestPgcryptoAESKeyIV(t *testing.T) {
	for _, n := range []int{0, 1, 15, 16, 17, 23, 24, 25, 31, 32, 33, 1024} {
		input := make([]byte, n)
		for i := range input {
			input[i] = byte(i + 128)
		}
		key, size := pgcryptoAESKey(string(input))
		wantSize := 16
		if n > 16 {
			wantSize = 24
		}
		if n > 24 {
			wantSize = 32
		}
		if size != wantSize {
			t.Fatal(n, size)
		}
		for i, got := range key {
			var want byte
			if i < n {
				want = input[i]
			}
			if got != want {
				t.Fatal("effective key", n, i, got, want)
			}
		}
	}
	for _, n := range []int{0, 1, 15, 16, 17, 64} {
		input := make([]byte, n)
		for i := range input {
			input[i] = byte(i + 128)
		}
		iv := pgcryptoAESIV(string(input))
		for i, got := range iv {
			var want byte
			if i < n {
				want = input[i]
			}
			if got != want {
				t.Fatal("effective IV", n, i, got, want)
			}
		}
	}
}

func TestPgcryptoAESPadding(t *testing.T) {
	for pad := 1; pad <= 16; pad++ {
		var block [16]byte
		for i := 16 - pad; i < 16; i++ {
			block[i] = byte(pad)
		}
		n, valid := pgcryptoRawUnpad(block[:])
		if !valid || n != 16-pad {
			t.Fatal(pad, n, valid)
		}
		if pad > 1 {
			block[16-pad] ^= 1
			if _, valid := pgcryptoRawUnpad(block[:]); valid {
				t.Fatal("incomplete padding validation", pad)
			}
		}
	}
	for _, pad := range []byte{0, 17, 255} {
		var block [16]byte
		block[15] = pad
		if _, valid := pgcryptoRawUnpad(block[:]); valid {
			t.Fatal("invalid pad accepted", pad)
		}
	}
}

func TestPgcryptoAESParser(t *testing.T) {
	for _, tc := range []struct {
		typ     string
		cipher  pgcryptoRawCipher
		mode    pgcryptoRawMode
		padding bool
	}{
		{"aes", pgcryptoCipherAES, pgcryptoModeCBC, true},
		{"rijndael-cbc", pgcryptoCipherAES, pgcryptoModeCBC, true},
		{"aes-ecb//pad:pkcs/pad:none/", pgcryptoCipherAES, pgcryptoModeECB, false},
		{"rijndael-ecb/pad:none/pad:pkcs", pgcryptoCipherAES, pgcryptoModeECB, true},
		{"aes-cfb/pad:none", pgcryptoCipherExcluded, pgcryptoModeCBC, false},
		{"bf", pgcryptoCipherBF, pgcryptoModeCBC, true},
		{"blowfish-cbc", pgcryptoCipherBF, pgcryptoModeCBC, true},
		{"bf-ecb/pad:none", pgcryptoCipherBF, pgcryptoModeECB, false},
		{"blowfish-ecb", pgcryptoCipherBF, pgcryptoModeECB, true},
		{"bf-cfb", pgcryptoCipherBF, pgcryptoModeCFB64, true},
		{"blowfish-cfb/pad:bogus/pad:none", pgcryptoCipherBF, pgcryptoModeCFB64, false},
		{"des", pgcryptoCipherExcluded, pgcryptoModeCBC, true},
		{"cast5", pgcryptoCipherExcluded, pgcryptoModeCBC, true},
	} {
		got, err := parsePgcryptoRaw(tc.typ)
		if err != nil || got != (pgcryptoRawOptions{cipher: tc.cipher, mode: tc.mode, padding: tc.padding}) {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct{ typ, reason string }{
		{"unknown/pad", "Badly formatted type"}, {"aes-cfb/foo:x", "Unknown option"},
		{"aes-cfb/pad:bogus", "No such cipher algorithm"}, {"aes-128-cbc", "No such cipher algorithm"},
	} {
		_, err := parsePgcryptoRaw(tc.typ)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22023" || diagnostic.Message != "Cannot use \""+tc.typ+"\": "+tc.reason {
			t.Fatal(tc, err)
		}
	}
}

type aesCancelContext struct {
	context.Context
	calls, after int
}

func (c *aesCancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}

func TestPgcryptoAESCancellation(t *testing.T) {
	for _, fn := range []string{"encrypt_iv", "decrypt_iv"} {
		for _, after := range []int{1, 2, 3, 4} {
			ctx := &aesCancelContext{Context: context.Background(), after: after}
			got, err := pgcryptoRaw(ctx, fn, strings.Repeat("x", 131072), "key", "iv", "aes/pad:none")
			if !errors.Is(err, context.Canceled) || got != "" || ctx.calls != after {
				t.Fatal(fn, after, len(got), ctx.calls, err)
			}
		}
	}
}
