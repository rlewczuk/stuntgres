package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgcryptoBFFeatureSmoke(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		steps := bfConsumerSetup(t, p, c, r)
		// The same public procedures and fourteen genuine members, with actual
		// Blowfish bytes, not an AES/startup-only substitute for feature smoke.
		pgcryptoResult(t, p, c, r, "encrypt", "SELECT pgcrypto_part.encrypt('\\x0000000000000000'::bytea,'\\x0000000000000000'::bytea,'bf-ecb/pad:none')", "4ef997456198dd78")
		pgcryptoResult(t, p, c, r, "decrypt", "SELECT pgcrypto_part.decrypt('\\x4ef997456198dd78'::bytea,'\\x0000000000000000'::bytea,'bf-ecb/pad:none')", "0000000000000000")
		q := "SELECT pgcrypto_part.encrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "42f58af3b2c03f46")}}, []byte("foo"), []byte("0123456789"), []byte{}, "bf"))
		aesHeldResult(t, p, c, r, "encrypt_iv", q, "95c7e89322525d59", []byte("foo"), []byte("0123456"), []byte("abcd"), "bf")
		pgcryptoResult(t, p, c, r, "decrypt_iv", "SELECT pgcrypto_part.decrypt_iv('\\x95c7e89322525d59'::bytea,'0123456','abcd','bf')", "666f6f")
		for _, typ := range []string{"bf-cfb", "bf-cfb/pad:pkcs", "bf-cfb/pad:none"} {
			aesHeldResult(t, p, c, r, "encrypt_iv", q, bfCFBCipher, aesBytes(t, bfCFBPlain), aesBytes(t, bfCFBKey), aesBytes(t, bfCFBIV), typ)
			aesHeldResult(t, p, c, r, "decrypt_iv", "SELECT pgcrypto_part.decrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)", bfCFBPlain, aesBytes(t, bfCFBCipher), aesBytes(t, bfCFBKey), aesBytes(t, bfCFBIV), typ)
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT length(pgcrypto_part.encrypt_iv($1::bytea,$2::bytea,$3::bytea,'bf-cfb')) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(29)}}, aesBytes(t, bfCFBPlain), aesBytes(t, bfCFBKey), aesBytes(t, bfCFBIV)))
		fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{nil}}, []byte("foo"), []byte("k"), nil, "bf-ecb"))
		for _, v := range []struct{ typ, code, message string }{
			{"bf/pad", "22023", `Cannot use "bf/pad": Badly formatted type`},
			{"bf/pad:none", "39000", "encrypt_iv error: Encryption failed"},
		} {
			fixture.RunStep(t, p.Ctx, held, r, fixture.ImplicitError(q, fixture.Diagnostic{Code: v.code, Message: v.message}, []byte("foo"), []byte("k"), []byte{}, v.typ))
		}
		fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "95c7e89322525d59")}}, []byte("foo"), []byte("0123456"), []byte("abcd"), "bf"))
		if !pg {
			bfRetainedControls(t, p, c, r, steps)
		}
	})
}
