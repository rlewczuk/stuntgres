package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgcryptoBFKeyAndIV(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		// These published short keys distinguish unsigned cyclic raw expansion
		// from bcrypt's terminating NUL and AES's zero-padding.
		for _, v := range []struct{ key, ct string }{{"f0", "f9ad597c49db005e"}, {"f0e1", "e91d21c1d961a6d6"}, {"f0e1d2", "e9c2b70a1bc65cf3"}} {
			bfVector(t, p, c, r, "fedcba9876543210", v.key, "", "bf-ecb/pad:none", v.ct)
		}
		// Pinned18.6 observations, including empty == one zero byte.
		keyCiphertexts := map[int]string{
			0: "09a5aa8371843981", 1: "09a5aa8371843981",
			2: "4288d56ab9470053", 3: "3c648bba5e3ac616",
			55: "bf03ff5e6467fa82", 56: "4f6b2acb8a4bf891",
			57: "4f6b2acb8a4bf891", 1024: "4f6b2acb8a4bf891",
		}
		for _, n := range []int{0, 1, 2, 3, 55, 56, 57, 1024} {
			bfVector(t, p, c, r, "fedcba9876543210", hex.EncodeToString(aesSequence(n)), "", "bf-ecb/pad:none", keyCiphertexts[n])
		}
		full := aesBytes(t, bfFullKey)
		for _, key := range [][]byte{full, append(append([]byte{}, full...), 0xff), append(append([]byte{}, full...), bytes.Repeat([]byte{0x80}, 1024)...)} {
			bfVector(t, p, c, r, "fedcba9876543210", hex.EncodeToString(key), "", "bf-ecb/pad:none", "c04504012e4e1f53")
		}
		for _, v := range []struct{ key, ciphertext string }{
			{"8000ff", "0278e086aa7f22cf"}, {"8000ff00", "68cef14c2cfeb063"},
		} {
			bfVector(t, p, c, r, "fedcba9876543210", v.key, "", "bf-ecb/pad:none", v.ciphertext)
		}
		ivCiphertexts := map[int]string{
			0: "80975e02431630d3", 1: "80975e02431630d3", 7: "e0704eed2b3134a8",
			8: "9a812351b0785994", 9: "9a812351b0785994", 64: "9a812351b0785994",
		}
		for _, n := range []int{0, 1, 7, 8, 9, 64} {
			iv := aesSequence(n)
			effective := make([]byte, 8)
			copy(effective, iv)
			for _, value := range [][]byte{iv, effective} {
				bfVector(t, p, c, r, "666f6f", "30313233343536", hex.EncodeToString(value), "bf-cbc", ivCiphertexts[n])
				bfVector(t, p, c, r, "0000000000000000", "0000000000000000", hex.EncodeToString(value), "bf-ecb/pad:none", "4ef997456198dd78")
			}
		}
		// The released short IV witness has an independently specified result.
		for _, iv := range []string{"61626364", "6162636400000000", "6162636400000000ff"} {
			bfVector(t, p, c, r, "666f6f", "30313233343536", iv, "bf", "95c7e89322525d59")
		}
	})
}

func TestReferencePgcryptoBFPaddingAndDiagnostics(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, mode := range []string{"cbc", "ecb"} {
			// Pinned18.6 literal ciphertexts for the binary 00/80/ff pattern.
			padding := map[int]string{
				0: "1871949bb2311c8e", 1: "ab074a6146500998", 7: "19c92a9835e08361",
				8: "08f855b8a66e3c7aae6114ab6800d178", 9: "08f855b8a66e3c7a2de4671c4aa0ed81",
				15: "08f855b8a66e3c7a88c2f28cfd19f1da", 16: "08f855b8a66e3c7aef025dcabda1e5f22b249f3c2f7bfcff",
			}
			if mode == "ecb" {
				padding[8], padding[9] = "08f855b8a66e3c7a1871949bb2311c8e", "08f855b8a66e3c7a35684ad2db2c864c"
				padding[15], padding[16] = "08f855b8a66e3c7a1fb4f668bb2b3ec5", "08f855b8a66e3c7a5b003eb50e28bff71871949bb2311c8e"
			}
			for _, n := range []int{0, 1, 7, 8, 9, 15, 16} {
				data := make([]byte, n)
				for i := range data {
					data[i] = []byte{0, 128, 255}[i%3]
				}
				bfVector(t, p, c, r, hex.EncodeToString(data), "666f6f", "", "bf-"+mode, padding[n])
			}
			for _, fn := range []string{"encrypt", "decrypt", "encrypt_iv", "decrypt_iv"} {
				q := "SELECT " + fn + "($1::bytea,$2::bytea,$3)"
				args := []any{[]byte{}, []byte("foo"), "bf-" + mode + "/pad:none"}
				if strings.HasSuffix(fn, "_iv") {
					q = "SELECT " + fn + "($1::bytea,$2::bytea,$3::bytea,$4)"
					args = []any{args[0], args[1], []byte("abcd"), args[2]}
				}
				pgcryptoResult(t, p, c, r, fn, q, "", args...)
				message := fn + " error: Encryption failed"
				if strings.HasPrefix(fn, "decrypt") {
					message = fn + " error: Decryption failed"
				}
				for _, n := range []int{1, 7, 9} {
					args[0] = bytes.Repeat([]byte{0x80}, n)
					aesErrorRoutes(t, p, c, r, q, fixture.Diagnostic{Code: "39000", Message: message}, args...)
				}
				if strings.HasPrefix(fn, "decrypt") {
					args[0], args[len(args)-1] = []byte{}, "bf-"+mode
					aesErrorRoutes(t, p, c, r, q, fixture.Diagnostic{Code: "39000", Message: message}, args...)
					for _, n := range []int{7, 9} {
						args[0] = bytes.Repeat([]byte{0x80}, n)
						aesErrorRoutes(t, p, c, r, q, fixture.Diagnostic{Code: "39000", Message: message}, args...)
					}
				}
			}
			// Each explicit PKCS suffix has an oracle-observed encrypt-none
			// ciphertext. Decryption never computes its own golden input.
			legal := []string{"7255628ceda00d34", "15038f8d260eff31", "7bd411c909440408", "9b47b8fed9e4ede8", "8b9a03a1ffdd2a07", "791244bb011261c7", "be6d1f9877b6d129", "1871949bb2311c8e"}
			for i, ciphertext := range legal {
				pad := i + 1
				block := bytes.Repeat([]byte{0xa5}, 8)
				copy(block[8-pad:], bytes.Repeat([]byte{byte(pad)}, pad))
				bfVector(t, p, c, r, hex.EncodeToString(block), "666f6f", "", "bf-"+mode+"/pad:none", ciphertext)
				for _, fn := range []string{"decrypt", "decrypt_iv"} {
					q := "SELECT " + fn + "($1::bytea,'foo',$2)"
					args := []any{aesBytes(t, ciphertext), "bf-" + mode}
					if fn == "decrypt_iv" {
						q = "SELECT decrypt_iv($1::bytea,'foo',''::bytea,$2)"
					}
					aesHeldResult(t, p, c, r, fn, q, hex.EncodeToString(block[:8-pad]), args...)
				}
			}
			// A byte resembling bad padding immediately BEFORE the legal run
			// is data. Preserve this oracle-discovered positive regression.
			bfVector(t, p, c, r, "0000000001030303", "666f6f", "", "bf-"+mode+"/pad:none", "cfe05dc46975bc21")
			aesHeldResult(t, p, c, r, "decrypt", "SELECT decrypt($1::bytea,'foo',$2)", "0000000001", aesBytes(t, "cfe05dc46975bc21"), "bf-"+mode)
			aesHeldResult(t, p, c, r, "decrypt_iv", "SELECT decrypt_iv($1::bytea,'foo',''::bytea,$2)", "0000000001", aesBytes(t, "cfe05dc46975bc21"), "bf-"+mode)
			for _, v := range []struct{ block, ciphertext string }{
				{"0000000000000000", "a6ba241e5c1b6cce"},
				{"0000000000000009", "7ac07ea01b7c2b79"},
				{"00000000000000ff", "f19783bef1d8472d"},
				{"0000000004040104", "cca407d8fbafa33b"},
			} {
				bfVector(t, p, c, r, v.block, "666f6f", "", "bf-"+mode+"/pad:none", v.ciphertext)
				aesErrorRoutes(t, p, c, r, "SELECT decrypt($1::bytea,'foo',$2)", fixture.Diagnostic{Code: "39000", Message: "decrypt error: Decryption failed"}, aesBytes(t, v.ciphertext), "bf-"+mode)
				aesErrorRoutes(t, p, c, r, "SELECT decrypt_iv($1::bytea,'foo',''::bytea,$2)", fixture.Diagnostic{Code: "39000", Message: "decrypt_iv error: Decryption failed"}, aesBytes(t, v.ciphertext), "bf-"+mode)
			}
		}
		// Known plaintext zero has pad byte zero: fixed published ciphertext,
		// not a native encrypt/decrypt round trip.
		aesErrorRoutes(t, p, c, r, "SELECT decrypt($1::bytea,$2::bytea,$3)", fixture.Diagnostic{Code: "39000", Message: "decrypt error: Decryption failed"}, aesBytes(t, "4ef997456198dd78"), make([]byte, 8), "bf-ecb")
		// CBC XOR with this explicit IV changes the observed zero plaintext
		// to 0000000000010303: the FIRST byte of its three-byte run is wrong.
		// No new cipher golden is needed; the source block is literal above.
		aesErrorRoutes(t, p, c, r, "SELECT decrypt_iv($1::bytea,'foo',$2::bytea,'bf-cbc')", fixture.Diagnostic{Code: "39000", Message: "decrypt_iv error: Decryption failed"}, aesBytes(t, "a6ba241e5c1b6cce"), aesBytes(t, "0000000000010303"))
		aesErrorRoutes(t, p, c, r, "SELECT encrypt('foo','key','bf/pad:none')", fixture.Diagnostic{Code: "39000", Message: "encrypt error: Encryption failed"})
		bfAtomicity(t, p, c, r)
	})
}

func TestReferencePgcryptoBFTypeGrammar(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, typ := range []string{"bf", "blowfish", "bf-cbc", "BLOWFISH-CBC/PAD:PKCS", "bf/", "bf//pad:pkcs/", "bf/pad:none/pad:pkcs"} {
			pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','0123456789',$1)", "42f58af3b2c03f46", typ)
		}
		for _, typ := range []string{"bf-ecb/pad:none", "bf-ecb//pad:none/", "bf-ecb/pad:pkcs/pad:none"} {
			bfVector(t, p, c, r, "0000000000000000", "0000000000000000", "", typ, "4ef997456198dd78")
		}
		cases := []struct{ typ, normalized, reason string }{
			{"bf/pad", "bf/pad", "Badly formatted type"}, {"bf/garbage", "bf/garbage", "Badly formatted type"},
			{"bf/foo:x", "bf/foo:x", "Unknown option"}, {"unknown/pad", "unknown/pad", "Badly formatted type"}, {"unknown/foo:x", "unknown/foo:x", "Unknown option"},
			{"bf/pad/pad:pkcs", "bf/pad/pad:pkcs", "Badly formatted type"}, {"bf/foo:x/pad:pkcs", "bf/foo:x/pad:pkcs", "Unknown option"},
			{strings.Repeat("B", 70), strings.Repeat("b", 63), "No such cipher algorithm"},
			{strings.Repeat("B", 62) + "éZ", strings.Repeat("b", 62), "No such cipher algorithm"}, {"ÄBF", "Äbf", "No such cipher algorithm"},
		}
		for _, typ := range []string{"", " bf", "bf ", "bf/pad:", "bf/pad:bogus", "bf-cbc/pad:none:extra", "bf-cfb/pad:bogus"} {
			cases = append(cases, struct{ typ, normalized, reason string }{typ, typ, "No such cipher algorithm"})
		}
		for _, tc := range cases {
			aesErrorRoutes(t, p, c, r, "SELECT encrypt('foo','0123456789',$1)", fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf("Cannot use %q: %s", tc.normalized, tc.reason)}, tc.typ)
		}
		bfVector(t, p, c, r, bfCFBPlain, bfCFBKey, bfCFBIV, "bf-cfb/pad:bogus/pad:none", bfCFBCipher)
		typ := "bf/pad:pkcs" + strings.Repeat("/", 53) + "pad:none"
		pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','0123456789',$1)", "42f58af3b2c03f46", typ)
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, pg, &notices)
		for _, tc := range cases {
			fixture.RunStep(t, p.Ctx, nc, nr, fixture.ImplicitError("SELECT encrypt('foo','0123456789',$1)", fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf("Cannot use %q: %s", tc.normalized, tc.reason)}, tc.typ))
		}
		if len(notices) != 0 {
			t.Fatalf("unexpected normalization notices %#v", notices)
		}
		aesErrorRoutes(t, p, c, r, "SELECT encrypt('foo','key','bf/pad')", fixture.Diagnostic{Code: "22023", Message: `Cannot use "bf/pad": Badly formatted type`})
	})
}

func bfAtomicity(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE TABLE pgcrypto_bf_input(id integer,p bytea)", 0))
	run(fixture.Exec("INSERT INTO pgcrypto_bf_input VALUES(1,'12345678'),(2,'bad')", 2))
	run(fixture.Exec("CREATE TABLE pgcrypto_bf_atomic(p bytea)", 0))
	q := "INSERT INTO pgcrypto_bf_atomic SELECT encrypt(p,'foo','bf/pad:none') FROM pgcrypto_bf_input ORDER BY id"
	failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "39000", Message: "encrypt error: Encryption failed"})
	run(failure)
	run(fixture.Prepared(failure))
	count := fixture.Select("SELECT count(*) FROM pgcrypto_bf_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
	run(count)
	run(fixture.Exec("BEGIN", 0))
	run(failure)
	run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
	run(fixture.Exec("ROLLBACK", 0))
	run(count)
	pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','0123456789','bf')", "42f58af3b2c03f46")
}
