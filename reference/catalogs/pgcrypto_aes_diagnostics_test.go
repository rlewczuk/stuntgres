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

func aesErrorRoutes(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, q string, d fixture.Diagnostic, args ...any) {
	t.Helper()
	// Assert every exposed logical field independently on each execution route.
	t.Run(fmt.Sprintf("%s %v", q, args), func(t *testing.T) {
		s := fixture.ImplicitError(q, d, args...)
		s.ErrorStage = "execute"
		t.Run("direct", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
		t.Run("prepared", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s)) })
		t.Run("held", func(t *testing.T) {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("unexpected prepare diagnostic", fixture.ExtractDiagnostic(t, err))
			}
			defer stmt.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: stmt, SQL: q}, r, s)
		})
	})
}

func TestReferencePgcryptoAESPaddingAndDiagnostics(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		// Independent pinned PostgreSQL18.0 observations from root tests-1.md.
		padding := []struct {
			n        int
			cbc, ecb string
		}{
			{0, "b48cc3338a2eb293b6007ef72c360d48", "b48cc3338a2eb293b6007ef72c360d48"},
			{1, "b18b0583d5455e2c989c59d48b3fd331", "b18b0583d5455e2c989c59d48b3fd331"},
			{15, "3a222a415e9edb8ae265b19290d728ae", "3a222a415e9edb8ae265b19290d728ae"},
			{16, "67d6b5ce972f3842146115fc4d6719565044bfb8cc8df9f503d4e969030f7f5d", "67d6b5ce972f3842146115fc4d671956b48cc3338a2eb293b6007ef72c360d48"},
			{17, "67d6b5ce972f3842146115fc4d6719560d048e71186e20bdad021d95a53a0709", "67d6b5ce972f3842146115fc4d67195667431864f09f62a2b9a55b4a35631ccf"},
			{31, "67d6b5ce972f3842146115fc4d671956a5811a8c056b1a8bd2e6ad8bea9021d3", "67d6b5ce972f3842146115fc4d671956351334eb829ea654cda33a56b21bab0f"},
			{32, "67d6b5ce972f3842146115fc4d671956407eb096ae1bc9f43c24bee52607cc0924611f4d91efba9d7333472a9de49d5e", "67d6b5ce972f3842146115fc4d6719560c5c0479c3657f4e0184a8be2116c43db48cc3338a2eb293b6007ef72c360d48"},
		}
		for _, v := range padding {
			data := make([]byte, v.n)
			for i := range data {
				data[i] = []byte{0, 128, 255}[i%3]
			}
			for _, mode := range []string{"cbc", "ecb"} {
				expected := v.cbc
				if mode == "ecb" {
					expected = v.ecb
				}
				aesHeldResult(t, p, c, r, "encrypt", "SELECT encrypt($1::bytea,$2::bytea,$3)", expected, data, []byte("foo"), "aes-"+mode)
				aesHeldResult(t, p, c, r, "decrypt", "SELECT decrypt($1::bytea,$2::bytea,$3)", hex.EncodeToString(data), aesBytes(t, expected), []byte("foo"), "aes-"+mode)
			}
		}
		for _, mode := range []string{"cbc", "ecb"} {
			for _, fn := range []string{"encrypt", "encrypt_iv", "decrypt", "decrypt_iv"} {
				q := "SELECT " + fn + "($1::bytea,$2::bytea,$3)"
				args := []any{[]byte{}, []byte("foo"), "aes-" + mode + "/pad:none"}
				if strings.HasSuffix(fn, "_iv") {
					q = "SELECT " + fn + "($1::bytea,$2::bytea,$3::bytea,$4)"
					args = []any{args[0], args[1], []byte("abcd"), args[2]}
				}
				pgcryptoResult(t, p, c, r, fn, q, "", args...)
				message := fn + " error: Encryption failed"
				if strings.HasPrefix(fn, "decrypt") {
					message = fn + " error: Decryption failed"
				}
				for _, n := range []int{1, 15, 17} {
					args[0] = bytes.Repeat([]byte{128}, n)
					aesErrorRoutes(t, p, c, r, q, fixture.Diagnostic{Code: "39000", Message: message}, args...)
				}
				if strings.HasPrefix(fn, "decrypt") {
					args[0] = []byte{}
					args[len(args)-1] = "aes-" + mode
					aesErrorRoutes(t, p, c, r, q, fixture.Diagnostic{Code: "39000", Message: message}, args...)
					args[0] = []byte{1}
					aesErrorRoutes(t, p, c, r, q, fixture.Diagnostic{Code: "39000", Message: message}, args...)
				}
			}
			for _, ending := range []struct {
				suffix []byte
				ct     string
			}{
				{[]byte{1, 1}, "687657aa4edd81aed89b5feaaf61624a"},
				{[]byte{2}, "89a668653602ab56e13214b246defb12"},
			} {
				data := make([]byte, 16)
				copy(data[16-len(ending.suffix):], ending.suffix)
				pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt($1::bytea,$2::bytea,$3)", ending.ct, data, []byte("foo"), "aes-"+mode+"/pad:none")
				aesHeldResult(t, p, c, r, "decrypt", "SELECT decrypt($1::bytea,$2::bytea,$3)", hex.EncodeToString(data), aesBytes(t, ending.ct), []byte("foo"), "aes-"+mode+"/pad:none")
			}
		}
		aesErrorRoutes(t, p, c, r, "SELECT decrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)", fixture.Diagnostic{Code: "39000", Message: "decrypt_iv error: Decryption failed"}, aesBytes(t, "a21a9c15231465964e3396d32095e67eb52bab05f556a581621dee1b85385789"), []byte("0123456"), []byte("abcd"), "aes")
		aesErrorRoutes(t, p, c, r, "SELECT decrypt($1::bytea,$2::bytea,$3)", fixture.Diagnostic{Code: "39000", Message: "decrypt error: Decryption failed"}, aesBytes(t, aesNIST[0].ecb[:32]), aesBytes(t, aesNIST[0].key), "aes-ecb")
		for _, last := range []byte{0x25, 0x27} {
			iv := aesBytes(t, aesIV)
			iv[15] = last
			aesErrorRoutes(t, p, c, r, "SELECT decrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)", fixture.Diagnostic{Code: "39000", Message: "decrypt_iv error: Decryption failed"}, aesBytes(t, aesNIST[0].cbc[:32]), aesBytes(t, aesNIST[0].key), iv, "aes")
		}
	})
}

func TestReferencePgcryptoAESTypeGrammar(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, typ := range []string{"aes", "aes-cbc", "AES-CBC/PAD:PKCS", "aes/", "aes//pad:pkcs/", "aes/pad:none/pad:pkcs", "rijndael", "rijndael-cbc"} {
			t.Run("valid_"+typ, func(t *testing.T) {
				pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','0123456789',$1)", "f397f03d2819b7172b68d0706fda4693", typ)
			})
		}
		for _, typ := range []string{"aes-ecb/pad:none", "AES-ECB/PAD:NONE", "aes-ecb//pad:none/", "aes-ecb/pad:pkcs/pad:none", "rijndael-ecb/pad:none"} {
			pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt($1::bytea,$2::bytea,$3)", aesNIST[0].ecb, aesBytes(t, aesPlain), aesBytes(t, aesNIST[0].key), typ)
		}
		cases := []struct{ typ, normalized, reason string }{
			{"aes/pad", "aes/pad", "Badly formatted type"}, {"aes/garbage", "aes/garbage", "Badly formatted type"},
			{"aes/foo:x", "aes/foo:x", "Unknown option"}, {"unknown/pad", "unknown/pad", "Badly formatted type"}, {"unknown/foo:x", "unknown/foo:x", "Unknown option"},
			{strings.Repeat("A", 70), strings.Repeat("a", 63), "No such cipher algorithm"},
			{strings.Repeat("A", 62) + "éZ", strings.Repeat("a", 62), "No such cipher algorithm"},
			{"ÄAES", "Äaes", "No such cipher algorithm"},
		}
		for _, typ := range []string{"", " aes", "aes ", "aes/pad:", "aes/pad:bogus", "aes-cbc/pad:none:extra", "aes-128", "aes256", "aes-cbc-pkcs", "aes-128-cbc", "aes-cfb/pad:bogus", "bf/pad:bogus"} {
			cases = append(cases, struct{ typ, normalized, reason string }{typ, typ, "No such cipher algorithm"})
		}
		for _, tc := range cases {
			q := "SELECT encrypt('foo','0123456789',$1)"
			aesErrorRoutes(t, p, c, r, q, fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf("Cannot use %q: %s", tc.normalized, tc.reason)}, tc.typ)
		}
		// Truncation clips the final option before parsing; the first pad wins.
		typ := "aes/pad:pkcs" + strings.Repeat("/", 52) + "pad:none"
		pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','0123456789',$1)", "f397f03d2819b7172b68d0706fda4693", typ)
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, pg, &notices)
		for _, tc := range cases {
			t.Run("notice_"+tc.typ, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, nc, nr, fixture.ImplicitError("SELECT encrypt('foo','0123456789',$1)", fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf("Cannot use %q: %s", tc.normalized, tc.reason)}, tc.typ))
			})
		}
		if len(notices) != 0 {
			t.Fatalf("unexpected normalization notices %#v", notices)
		}
	})
}

func TestReferencePgcryptoAESNullAndDemand(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, fn := range []string{"encrypt", "decrypt", "encrypt_iv", "decrypt_iv"} {
			q := "SELECT " + fn + "($1::bytea,$2::bytea,$3)"
			base := []any{[]byte("foo"), []byte("key"), "NO_SUCH_CIPHER"}
			if strings.HasSuffix(fn, "_iv") {
				q = "SELECT " + fn + "($1::bytea,$2::bytea,$3::bytea,$4)"
				base = []any{base[0], base[1], []byte("iv"), base[2]}
			}
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for i := range base {
				args := append([]any(nil), base...)
				args[i] = nil
				s := fixture.Select(q, []string{fn}, []string{"BYTEA"}, [][]any{{nil}}, args...)
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: stmt, SQL: q}, r, s)
			}
			stmt.Close()
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT encrypt_iv('foo','key',NULL,'aes-ecb')", []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{nil}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT encrypt($1::bytea,'foo','aes')", []string{"encrypt"}, []string{"BYTEA"}, [][]any{{nil}}, []byte(nil)))
		pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt($1::bytea,'foo','aes')", "b48cc3338a2eb293b6007ef72c360d48", []byte{})
		pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt($1::bytea,'foo','aes')", "b48cc3338a2eb293b6007ef72c360d48", "")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SEQUENCE pgcrypto_aes_effect", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_aes_null(v bytea)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_aes_null VALUES(NULL)", 1))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT encrypt_iv(v,'k','iv',nextval('pgcrypto_aes_effect')::text) FROM pgcrypto_aes_null", []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{nil}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT currval('pgcrypto_aes_effect')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		aesErrorRoutes(t, p, c, r, "SELECT encrypt_iv($1::bytea,'k','iv',($2::integer/$3::integer)::text)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, nil, 1, 0)
		aesErrorRoutes(t, p, c, r, "SELECT encrypt(NULL,'k',(1/0)::text)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT CASE WHEN false THEN encrypt('foo','k','bad') ELSE NULL::bytea END", []string{"case"}, []string{"BYTEA"}, [][]any{{nil}}))
	})
}

func TestReferencePgcryptoAESUnsupported(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, typ := range []string{"aes-cfb", "rijndael-cfb", "des", "des-cbc", "des-ecb", "3des", "3des-cbc", "3des-ecb", "des3-cbc", "des3-ecb", "cast5", "cast5-cbc", "cast5-ecb"} {
			if pg {
				// Pinned18.6 default-plus-legacy provider observations; native
				// exclusions below are intentionally unchanged.
				switch typ {
				case "aes-cfb", "rijndael-cfb":
					pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','key',$1)", "e5b6f9", typ)
				case "3des", "3des-cbc", "3des-ecb", "des3-cbc", "des3-ecb":
					pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','key',$1)", "bfe34a5f2fe205ce", typ)
				default:
					expected := "bfe34a5f2fe205ce"
					if strings.HasPrefix(typ, "cast5") {
						expected = "2dfb96d858cf0e57"
					}
					aesHeldResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','key',$1)", expected, typ)
				}
				continue
			}
			for _, fn := range []string{"encrypt", "decrypt", "encrypt_iv", "decrypt_iv"} {
				q := "SELECT " + fn + "('foo','key',$1)"
				if strings.HasSuffix(fn, "_iv") {
					q = "SELECT " + fn + "('foo','key','iv',$1)"
				}
				aesErrorRoutes(t, p, c, r, q, fixture.Diagnostic{Code: "0A000", Message: fn + ": unsupported cipher algorithm"}, typ)
			}
		}
	})
}
