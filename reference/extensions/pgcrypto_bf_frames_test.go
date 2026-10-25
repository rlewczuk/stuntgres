package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgcryptoBFOverloadsAndFrames(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, v := range []struct {
			name, q, expected string
			args              []any
		}{
			{"encrypt", "SELECT encrypt($1,$2,$3)", "42f58af3b2c03f46", []any{[]byte("foo"), []byte("0123456789"), "bf"}},
			{"decrypt", "SELECT decrypt($1,$2,$3)", "666f6f", []any{aesBytes(t, "42f58af3b2c03f46"), []byte("0123456789"), "bf"}},
			{"encrypt_iv", "SELECT encrypt_iv($1,$2,$3,$4)", "95c7e89322525d59", []any{[]byte("foo"), []byte("0123456"), []byte("abcd"), "bf"}},
			{"decrypt_iv", "SELECT decrypt_iv($1,$2,$3,$4)", "666f6f", []any{aesBytes(t, "95c7e89322525d59"), []byte("0123456"), []byte("abcd"), "bf"}},
		} {
			aesHeldResult(t, p, c, r, v.name, v.q, v.expected, v.args...)
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE DOMAIN pgcrypto_bf_binary AS bytea", 0))
		pgcryptoResult(t, p, c, r, "encrypt_iv", "SELECT encrypt_iv('foo'::pgcrypto_bf_binary,'0123456'::pgcrypto_bf_binary,'abcd'::pgcrypto_bf_binary,'bf')", "95c7e89322525d59")
		for _, tc := range []struct{ q, name, signature string }{
			{"SELECT encrypt('foo'::text,'k','bf')", "encrypt", "text, unknown, unknown"},
			{"SELECT decrypt('foo','k'::text,'bf')", "decrypt", "unknown, text, unknown"},
			{"SELECT encrypt_iv('foo','k','iv'::text,'bf')", "encrypt_iv", "unknown, unknown, text, unknown"},
			{"SELECT decrypt_iv('foo','k')", "decrypt_iv", "unknown, unknown"},
		} {
			s := extensionMissing(tc.q, tc.name, tc.signature)
			fixture.RunStep(t, p.Ctx, c, r, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		for _, fn := range []string{"encrypt", "decrypt", "encrypt_iv", "decrypt_iv"} {
			q := "SELECT " + fn + "($1::bytea,$2::bytea,$3)"
			base := []any{[]byte("bad"), []byte("key"), "bf/pad:bogus"}
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
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT encrypt_iv('foo','k',NULL,'bf-ecb')", []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{nil}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SEQUENCE pgcrypto_bf_effect", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_bf_null(v bytea)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_bf_null VALUES(NULL)", 1))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT encrypt_iv(v,'k','iv',nextval('pgcrypto_bf_effect')::text) FROM pgcrypto_bf_null", []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{nil}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT currval('pgcrypto_bf_effect')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		aesErrorRoutes(t, p, c, r, "SELECT encrypt_iv($1::bytea,'k','iv',($2::integer/$3::integer)::text)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}, nil, 1, 0)
		aesErrorRoutes(t, p, c, r, "SELECT encrypt(NULL,'k',(1/0)::text)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		q := "SELECT encrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Statement: stmt, SQL: q}
		for range 2 {
			for _, v := range []struct {
				data, key, iv []byte
				typ, expected string
			}{
				{[]byte("foo"), []byte("0123456"), []byte("abcd"), "aes", "2c24cb7da91d6d5699801268b0f5adad"},
				{[]byte("foo"), []byte("0123456"), []byte("abcd"), "bf", "95c7e89322525d59"},
				{aesBytes(t, "fedcba9876543210"), aesBytes(t, "f0"), []byte("ignored long IV"), "bf-ecb/pad:none", "f9ad597c49db005e"},
				{aesBytes(t, "fedcba9876543210"), aesBytes(t, bfFullKey), []byte{}, "bf-ecb/pad:none", "c04504012e4e1f53"},
				{aesBytes(t, bfCFBPlain), aesBytes(t, bfCFBKey), aesBytes(t, bfCFBIV), "bf-cfb/pad:pkcs", bfCFBCipher},
			} {
				s := fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, v.expected)}}, v.data, v.key, v.iv, v.typ)
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
				fixture.RunStep(t, p.Ctx, held, r, s)
			}
			fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{nil}}, []byte("foo"), []byte("k"), nil, "bf-ecb"))
			fixture.RunStep(t, p.Ctx, held, r, fixture.ImplicitError(q, fixture.Diagnostic{Code: "22023", Message: `Cannot use "bf/pad:bogus": No such cipher algorithm`}, []byte("foo"), []byte("k"), []byte{}, "bf/pad:bogus"))
			fixture.RunStep(t, p.Ctx, held, r, fixture.ImplicitError(q, fixture.Diagnostic{Code: "39000", Message: "encrypt_iv error: Encryption failed"}, []byte("foo"), []byte("k"), []byte{}, "bf/pad:none"))
		}
		fixture.RunStep(t, p.Ctx, held, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "95c7e89322525d59")}}, []byte("foo"), []byte("0123456"), []byte("abcd"), "bf"))
		bfFrameMetadata(t, p, c, r, pg, stmt, q)
	})
}

func bfFrameMetadata(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, held *sql.Stmt, heldSQL string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE pgcrypto_bf_empty(v bytea)", 0))
	for _, q := range []string{"SELECT encrypt(NULL,'foo','bf')", "SELECT encrypt(v,'foo','bf') FROM pgcrypto_bf_empty"} {
		rows, err := c.QueryContext(p.Ctx, q)
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
		count := 0
		for rows.Next() {
			var value any
			if err := rows.Scan(&value); err != nil || value != nil {
				rows.Close()
				t.Fatal(value, err)
			}
			count++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		want := 0
		if strings.Contains(q, "NULL") {
			want = 1
		}
		if count != want || columns[0].DatabaseTypeName() != "BYTEA" {
			t.Fatal("NULL/empty rows or retained metadata", count, columns)
		}
	}
	pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt($1::bytea,'foo','bf')", "1871949bb2311c8e", []byte{})
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT encrypt($1::bytea,'foo','bf')", []string{"encrypt"}, []string{"BYTEA"}, [][]any{{nil}}, []byte(nil)))
	if pg {
		return // Native ordinary-context contract, not a fabricated PG SQLSTATE.
	}
	var empty []byte
	if err := c.QueryRowContext(p.Ctx, "SELECT encrypt('','foo','bf/pad:none')").Scan(&empty); err != nil || empty == nil || len(empty) != 0 {
		t.Fatal("owned nonnull empty BYTEA", empty, err)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if rows, err := c.QueryContext(ctx, heldSQL, []byte("foo"), []byte("k"), []byte{}, "bf"); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("BF precancel direct", rows, err)
	}
	if stmt, err := c.PrepareContext(ctx, heldSQL); !errors.Is(err, context.Canceled) || stmt != nil {
		t.Fatal("BF precancel prepare", stmt, err)
	}
	if rows, err := held.QueryContext(ctx, []byte("foo"), []byte("k"), []byte{}, "bf"); !errors.Is(err, context.Canceled) || rows != nil {
		t.Fatal("BF precancel held", rows, err)
	}
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held, SQL: heldSQL}, r, fixture.Select(heldSQL, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "95c7e89322525d59")}}, []byte("foo"), []byte("0123456"), []byte("abcd"), "bf"))
}
