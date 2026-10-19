package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Assert the complete selected manifest independently in every caller. Stock
// PostgreSQL's unrelated pgcrypto routines are intentionally not native members.
func pgcryptoRandomInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool, owner string) map[string]int64 {
	t.Helper()
	if _, err := c.ExecContext(p.Ctx, "SET search_path TO public"); err != nil {
		t.Fatal(err)
	}
	type member struct {
		ret    int64
		vol    string
		strict bool
	}
	want := map[string]member{
		"pgcrypto_part.digest(text,text)": {17, "i", true}, "pgcrypto_part.digest(bytea,text)": {17, "i", true},
		"pgcrypto_part.hmac(text,text,text)": {17, "i", true}, "pgcrypto_part.hmac(bytea,bytea,text)": {17, "i", true},
		"pgcrypto_part.crypt(text,text)": {25, "i", true}, "pgcrypto_part.gen_salt(text)": {25, "v", true}, "pgcrypto_part.gen_salt(text,integer)": {25, "v", true},
		"pgcrypto_part.encrypt(bytea,bytea,text)": {17, "i", true}, "pgcrypto_part.decrypt(bytea,bytea,text)": {17, "i", true},
		"pgcrypto_part.encrypt_iv(bytea,bytea,bytea,text)": {17, "i", true}, "pgcrypto_part.decrypt_iv(bytea,bytea,bytea,text)": {17, "i", true},
		"pgcrypto_part.gen_random_bytes(integer)": {17, "v", true}, "pgcrypto_part.gen_random_uuid()": {2950, "v", false}, "pgcrypto_part.fips_mode()": {16, "v", true},
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.oid::regprocedure::text,prorettype,provolatile,proisstrict,proparallel,r.rolname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='pgcrypto_part' AND proname IN ('digest','hmac','crypt','gen_salt','encrypt','decrypt','encrypt_iv','decrypt_iv','gen_random_bytes','gen_random_uuid','fips_mode')")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	used := map[int64]bool{}
	for rows.Next() {
		var id, ret int64
		var signature, vol, parallel, gotOwner string
		var strict bool
		if err := rows.Scan(&id, &signature, &ret, &vol, &strict, &parallel, &gotOwner); err != nil {
			t.Fatal(err)
		}
		m, ok := want[signature]
		if !ok || ids[signature] != 0 || id <= 0 || used[id] || ret != m.ret || vol != m.vol || strict != m.strict || parallel != "s" || gotOwner != owner {
			t.Fatal("installed member", id, signature, ret, vol, strict, parallel, gotOwner)
		}
		ids[signature] = id
		used[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(ids) != 14 {
		t.Fatal("fourteen selected members", ids)
	}
	var ext int64
	var version, namespace string
	var relocatable bool
	if err := c.QueryRowContext(p.Ctx, "SELECT e.oid,extversion,n.nspname,extrelocatable FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE extname='pgcrypto'").Scan(&ext, &version, &namespace, &relocatable); err != nil || ext <= 0 || version != "1.4" || namespace != "pgcrypto_part" || !relocatable {
		t.Fatal(ext, version, namespace, relocatable, err)
	}
	for _, id := range ids {
		var n int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend WHERE classid=1255 AND objid=$1 AND objsubid=0 AND refclassid=3079 AND refobjid=$2 AND refobjsubid=0 AND deptype='e'", id, ext).Scan(&n); err != nil || n != 1 {
			t.Fatal("extension membership", id, n, err)
		}
	}
	return ids
}

func TestReferencePgcryptoRandomIdentity(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		pgcryptoRandomUUIDQuery(t, p, c, "SELECT pg_catalog.gen_random_uuid() AS value", pg)
		for _, v := range []struct{ q, name, args string }{
			{"SELECT gen_random_bytes(1)", "gen_random_bytes", "integer"}, {"SELECT fips_mode()", "fips_mode", ""}, {"SELECT public.gen_random_uuid()", "public.gen_random_uuid", ""},
		} {
			run(extensionMissing(v.q, v.name, v.args))
		}
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		pgcryptoRandomInventory(t, p, c, pg, owner)
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pgcrypto_part' AND proname IN ('gen_random_bytes','gen_random_uuid','fips_mode')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		pgcryptoRandomUUIDQuery(t, p, c, "SELECT gen_random_uuid() AS value", pg)
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		pgcryptoRandomInventory(t, p, c, pg, owner)
		run(fixture.Exec("SET search_path TO pgcrypto_part,public", 0))
		for _, v := range []struct{ q, name, args string }{
			{"SELECT gen_random_bytes()", "gen_random_bytes", ""}, {"SELECT gen_random_bytes(1,2)", "gen_random_bytes", "integer, integer"},
			{"SELECT gen_random_bytes(1::bigint)", "gen_random_bytes", "bigint"}, {"SELECT gen_random_bytes(1::numeric)", "gen_random_bytes", "numeric"},
			{"SELECT gen_random_bytes('1'::text)", "gen_random_bytes", "text"}, {"SELECT gen_random_bytes(true)", "gen_random_bytes", "boolean"},
			{"SELECT \"GEN_RANDOM_BYTES\"(1)", "GEN_RANDOM_BYTES", "integer"}, {"SELECT gen_random_bytes(count => 1)", "gen_random_bytes", "count => integer"},
			{"SELECT gen_random_uuid(NULL)", "gen_random_uuid", "unknown"}, {"SELECT fips_mode(NULL)", "fips_mode", "unknown"},
		} {
			s := extensionMissing(v.q, v.name, v.args)
			run(s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			run(s)
		}
		run(fixture.Exec("CREATE USER pgcrypto_owner", 0))
		if pg {
			run(fixture.Exec("REVOKE USAGE ON SCHEMA pgcrypto_part FROM PUBLIC", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		run(extensionMissing("SELECT gen_random_bytes(1)", "gen_random_bytes", "integer"))
		run(fixture.ImplicitError("SELECT pgcrypto_part.gen_random_bytes(1)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pgcrypto_part", Position: "8"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Trusted non-superuser install splits extension and member owners.
		run(fixture.Exec("DROP EXTENSION pgcrypto", 0))
		run(fixture.Exec("ALTER SCHEMA pgcrypto_part OWNER TO pgcrypto_owner", 0))
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		quotedDatabase := `"` + strings.ReplaceAll(database, `"`, `""`) + `"`
		run(fixture.Exec("GRANT CREATE ON DATABASE "+quotedDatabase+" TO pgcrypto_owner", 0))
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
			if _, err := c.ExecContext(p.Ctx, "REVOKE CREATE ON DATABASE "+quotedDatabase+" FROM pgcrypto_owner"); err != nil {
				t.Error(err)
			}
		}()
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		pgcryptoRandomInventory(t, p, c, pg, owner)
		run(fixture.Select("SELECT r.rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE extname='pgcrypto'", []string{"rolname"}, []string{"NAME"}, [][]any{{"pgcrypto_owner"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
