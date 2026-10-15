package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func aesSequence(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

func TestReferencePgcryptoAESKeyAndIV(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		// Pinned PostgreSQL18.0 observations, root tests-1.md; these literal
		// expectations are independent of the native AES implementation.
		keyCiphertexts := map[int]string{
			0:    "c8a331ff8edd3db175e1545dbefb760b",
			1:    "c8a331ff8edd3db175e1545dbefb760b",
			15:   "79ecc30b84ca971478546ac3f45b5899",
			16:   "69c4e0d86a7b0430d8cdb78070b4c55a",
			17:   "05aee450d92b1d0b0dd8afdf15d72caf",
			23:   "7d913b87005817405672e37c8df4857c",
			24:   "dda97ca4864cdfe06eaf70a0ec0d7191",
			25:   "e75b822c0a12a0877678fef28bea4c2f",
			31:   "0c1cfc4ac798444ec8ac607f5d7afe62",
			32:   "8ea2b7ca516745bfeafc49904b496089",
			33:   "8ea2b7ca516745bfeafc49904b496089",
			1024: "8ea2b7ca516745bfeafc49904b496089",
		}
		keySQL := "SELECT encrypt($1::bytea,$2::bytea,$3)"
		keyStmt, err := c.PrepareContext(p.Ctx, keySQL)
		if err != nil {
			t.Fatal(err)
		}
		defer keyStmt.Close()
		for _, n := range []int{0, 1, 15, 16, 17, 23, 24, 25, 31, 32, 33, 1024} {
			key := aesSequence(n)
			size := 16
			if n > 16 {
				size = 24
			}
			if n > 24 {
				size = 32
			}
			effective := make([]byte, size)
			copy(effective, key)
			for _, k := range [][]byte{key, effective} {
				args := []any{aesBytes(t, "00112233445566778899aabbccddeeff"), k, "aes-ecb/pad:none"}
				pgcryptoResult(t, p, c, r, "encrypt", keySQL, keyCiphertexts[n], args...)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: keyStmt, SQL: keySQL}, r, fixture.Select(keySQL, []string{"encrypt"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, keyCiphertexts[n])}}, args...))
			}
		}
		ivCiphertexts := map[int]string{
			0:  "3ad77bb40d7a3660a89ecaf32466ef97b148c17f309ee692287ae57cf12add49c93d11bfaf08c5dc4d90b37b4dee002ba7356e1207bb406639e5e5ceb9a9ed93",
			1:  "3ad77bb40d7a3660a89ecaf32466ef97b148c17f309ee692287ae57cf12add49c93d11bfaf08c5dc4d90b37b4dee002ba7356e1207bb406639e5e5ceb9a9ed93",
			15: "0f908298e38475d78bb97e0651ac49a064e95d1eeac66716ae244e6502202f639721147e1d0f3f4074e93f0dbf49194a795f6d34fe0a243afa5075a06bc011f9",
			16: aesNIST[0].cbc,
			17: aesNIST[0].cbc,
			64: aesNIST[0].cbc,
		}
		ivSQL := "SELECT encrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)"
		ivStmt, err := c.PrepareContext(p.Ctx, ivSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer ivStmt.Close()
		for _, n := range []int{0, 1, 15, 16, 17, 64} {
			iv := aesSequence(n)
			effective := make([]byte, 16)
			copy(effective, iv)
			for _, value := range [][]byte{iv, effective} {
				args := []any{aesBytes(t, aesPlain), aesBytes(t, aesNIST[0].key), value, "aes-cbc/pad:none"}
				pgcryptoResult(t, p, c, r, "encrypt_iv", ivSQL, ivCiphertexts[n], args...)
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: ivStmt, SQL: ivSQL}, r, fixture.Select(ivSQL, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, ivCiphertexts[n])}}, args...))
				pgcryptoResult(t, p, c, r, "encrypt_iv", "SELECT encrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)", aesNIST[0].ecb, aesBytes(t, aesPlain), aesBytes(t, aesNIST[0].key), value, "aes-ecb/pad:none")
			}
		}
	})
}

func TestReferencePgcryptoAESOverloadsAndFrames(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, q := range []string{"SELECT encrypt($1,$2,$3)", "SELECT encrypt($1::bytea,$2::bytea,$3)", "SELECT ENCRYPT($1,$2,$3)"} {
			pgcryptoResult(t, p, c, r, "encrypt", q, "f397f03d2819b7172b68d0706fda4693", []byte("foo"), []byte("0123456789"), "aes")
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN pgcrypto_aes_binary AS bytea", 0))
		pgcryptoResult(t, p, c, r, "encrypt_iv", "SELECT encrypt_iv('foo'::pgcrypto_aes_binary,'0123456'::pgcrypto_aes_binary,'abcd'::pgcrypto_aes_binary,'aes')", "2c24cb7da91d6d5699801268b0f5adad")
		for _, tc := range []struct{ q, name, signature string }{
			{"SELECT encrypt('foo'::text,'k','aes')", "encrypt", "text, unknown, unknown"},
			{"SELECT decrypt('foo','k'::text,'aes')", "decrypt", "unknown, text, unknown"},
			{"SELECT encrypt_iv('foo','k','iv'::text,'aes')", "encrypt_iv", "unknown, unknown, text, unknown"},
			{"SELECT decrypt_iv('foo','k')", "decrypt_iv", "unknown, unknown"},
			{"SELECT \"Encrypt\"('foo','k','aes')", "Encrypt", "unknown, unknown, unknown"},
		} {
			s := extensionMissing(tc.q, tc.name, tc.signature)
			fixture.RunStep(t, p.Ctx, c, r, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		q := "SELECT encrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, v := range aesNIST {
			for _, mode := range []string{"cbc", "ecb"} {
				expected := v.cbc
				if mode == "ecb" {
					expected = v.ecb
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: stmt, SQL: q}, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, expected)}}, aesBytes(t, aesPlain), aesBytes(t, v.key), aesBytes(t, aesIV), "aes-"+mode+"/pad:none"))
			}
		}
		held := fixture.HeldStatement{Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{nil}}, []byte("foo"), []byte("0123456"), nil, "aes-ecb"))
		for _, typ := range []string{"aes", "rijndael-cbc", "AES-CBC/PAD:PKCS"} {
			fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "2c24cb7da91d6d5699801268b0f5adad")}}, []byte("foo"), []byte("0123456"), []byte("abcd"), typ))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_aes_empty(v bytea)", 0))
		for _, query := range []string{"SELECT encrypt(NULL,'k','aes')", "SELECT encrypt(v,'k','aes') FROM pgcrypto_aes_empty"} {
			rows, err := c.QueryContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if len(columns) != 1 || columns[0].Name() != "encrypt" || columns[0].DatabaseTypeName() != "BYTEA" || columns[0].ScanType() != reflect.TypeOf([]byte{}) {
				rows.Close()
				t.Fatal("BYTEA metadata", columns)
			}
			for rows.Next() {
				var value any
				if err := rows.Scan(&value); err != nil || value != nil {
					rows.Close()
					t.Fatal(value, err)
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
			if columns[0].DatabaseTypeName() != "BYTEA" {
				t.Fatal("retained metadata")
			}
		}
		if !pg {
			var empty []byte
			if err := c.QueryRowContext(p.Ctx, "SELECT encrypt('','foo','aes/pad:none')").Scan(&empty); err != nil || empty == nil || len(empty) != 0 {
				t.Fatal("owned nonnull empty BYTEA", empty, err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if rows, err := c.QueryContext(ctx, q, []byte("foo"), []byte("key"), []byte("iv"), "aes"); !errors.Is(err, context.Canceled) || rows != nil {
				t.Fatal("AES precancel Query", rows, err)
			}
			if stmt, err := c.PrepareContext(ctx, q); !errors.Is(err, context.Canceled) || stmt != nil {
				t.Fatal("AES precancel Prepare", stmt, err)
			}
		}
	})
}
