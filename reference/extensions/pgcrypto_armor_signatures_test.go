package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgcryptoArmorInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, schema string) map[string]int64 {
	t.Helper()
	// regprocedure presentation is independent of the installer's search_path.
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO public", 0))
	type declaration struct {
		result int64
		source string
		set    bool
		nargs  int64
	}
	want := map[string]declaration{
		schema + ".armor(bytea)":               {25, "pg_armor", false, 1},
		schema + ".armor(bytea,text[],text[])": {25, "pg_armor", false, 3},
		schema + ".dearmor(text)":              {17, "pg_dearmor", false, 1},
		schema + ".pgp_armor_headers(text)":    {2249, "pgp_armor_headers", true, 1},
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.oid::regprocedure::text,prorettype,prosrc,probin,provolatile,proisstrict,proparallel,proretset,pronargs FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND proname IN ('armor','dearmor','pgp_armor_headers')", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]int64{}
	used := map[int64]bool{}
	for rows.Next() {
		var id, result, nargs int64
		var signature, source, library, volatility, parallel string
		var strict, set bool
		if err := rows.Scan(&id, &signature, &result, &source, &library, &volatility, &strict, &parallel, &set, &nargs); err != nil {
			t.Fatal(err)
		}
		d, ok := want[signature]
		if !ok || id <= 0 || used[id] || ids[signature] != 0 || result != d.result || source != d.source || library != "$libdir/pgcrypto" || volatility != "i" || !strict || parallel != "s" || set != d.set || nargs != d.nargs {
			t.Fatal("armor declaration", id, signature, result, source, library, volatility, strict, parallel, set, nargs)
		}
		ids[signature], used[id] = id, true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 4 {
		t.Fatal("four documented armor declarations", ids)
	}
	rows.Close()
	for signature, id := range ids {
		var membership int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.objid=$1 AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='pgcrypto'", id).Scan(&membership); err != nil || membership != 1 {
			t.Fatal("armor member identity", signature, membership, err)
		}
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT proallargtypes::text,proargmodes::text,proargnames::text,prorows::bigint FROM pg_proc WHERE oid=$1", []string{"proallargtypes", "proargmodes", "proargnames", "prorows"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{"{25,25,25}", "{i,o,o}", "{\"\",key,value}", int64(1000)}}, ids[schema+".pgp_armor_headers(text)"]))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND proname IN ('armor','dearmor') AND proallargtypes IS NULL AND proargmodes IS NULL AND proargnames IS NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, schema))
	return ids
}

func TestReferencePgcryptoArmorSignatures(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		binding := func(s fixture.Step) {
			t.Helper()
			s.SelectRows, s.ErrorStage = true, "execute"
			t.Run("direct_binding", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			t.Run("prepared_binding", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, s) })
		}
		for _, tc := range []struct{ query, name, args string }{
			{"SELECT armor('test'::bytea)", "armor", "bytea"},
			{"SELECT dearmor('packet'::text)", "dearmor", "text"},
			{"SELECT * FROM pgp_armor_headers('packet'::text)", "pgp_armor_headers", "text"},
		} {
			s := extensionMissing(tc.query, tc.name, tc.args)
			if tc.name == "pgp_armor_headers" {
				s.Diagnostic.Position = "15"
			}
			binding(s)
		}
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		installed := pgcryptoRandomInventory(t, p, c, pg, owner)
		// Partition the eighteen non-armor declarations explicitly, not by
		// catalog query order; the shared inventory contains twenty-two.
		incumbents := []string{
			"pgcrypto_part.digest(text,text)", "pgcrypto_part.digest(bytea,text)",
			"pgcrypto_part.hmac(text,text,text)", "pgcrypto_part.hmac(bytea,bytea,text)",
			"pgcrypto_part.crypt(text,text)", "pgcrypto_part.gen_salt(text)", "pgcrypto_part.gen_salt(text,integer)",
			"pgcrypto_part.encrypt(bytea,bytea,text)", "pgcrypto_part.decrypt(bytea,bytea,text)",
			"pgcrypto_part.encrypt_iv(bytea,bytea,bytea,text)", "pgcrypto_part.decrypt_iv(bytea,bytea,bytea,text)",
			"pgcrypto_part.gen_random_bytes(integer)", "pgcrypto_part.gen_random_uuid()", "pgcrypto_part.fips_mode()",
			"pgcrypto_part.pgp_sym_encrypt(text,text)", "pgcrypto_part.pgp_sym_encrypt(text,text,text)",
			"pgcrypto_part.pgp_sym_decrypt(bytea,text)", "pgcrypto_part.pgp_sym_decrypt(bytea,text,text)",
		}
		priorIDs := map[int64]string{}
		for _, signature := range incumbents {
			id := installed[signature]
			if id <= 0 || priorIDs[id] != "" {
				t.Fatal("incumbent declaration identity", signature, id, priorIDs[id])
			}
			priorIDs[id] = signature
		}
		added := pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")
		if len(installed) != len(incumbents)+len(added) {
			t.Fatal("installed inventory is not eighteen non-armor plus four armor declarations", installed)
		}
		for signature, id := range added {
			if installed[signature] != id {
				t.Fatal("armor declaration changed installed identity", signature, installed[signature], id)
			}
			if prior, exists := priorIDs[id]; exists {
				t.Fatal("new declaration reused old identity", signature, prior)
			}
		}
		run(fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pgcrypto_part' AND proname IN ('digest','hmac','crypt','gen_salt','encrypt','decrypt','encrypt_iv','decrypt_iv','gen_random_bytes','gen_random_uuid','fips_mode','armor','dearmor','pgp_armor_headers','pgp_sym_encrypt','pgp_sym_decrypt')", []string{"count"}, []string{"INT8"}, [][]any{{int64(22)}}))
		pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT pgcrypto_part.armor($1)", pgcryptoArmorTest, []byte("test")))
		run(fixture.Exec("SET search_path TO pgcrypto_part,public", 0))
		pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorText("SELECT armor($1,$2,$3)", fixture.PgcryptoArmorZooka, []byte("zooka"), "{foo}", "{bar}"))
		pgcryptoArmorBoth(t, p, c, r, pgcryptoArmorBytes("SELECT dearmor($1::varchar)", []byte("test"), pgcryptoArmorTest))
		pgcryptoArmorBoth(t, p, c, r, fixture.PgcryptoArmorHeaders("SELECT * FROM pgp_armor_headers($1)", [][]any{{"foo", "bar"}}, fixture.PgcryptoArmorZooka))
		for _, tc := range []struct{ query, name, args string }{
			{"SELECT armor()", "armor", ""},
			{"SELECT armor('test'::bytea,'{}'::text[])", "armor", "bytea, text[]"},
			{"SELECT armor('test'::bytea,'{}'::text[],'{}'::text[],NULL)", "armor", "bytea, text[], text[], unknown"},
			{"SELECT armor('test'::text)", "armor", "text"},
			{"SELECT armor('test'::bytea,ARRAY[1],ARRAY[2])", "armor", "bytea, integer[], integer[]"},
			{"SELECT dearmor('test'::bytea)", "dearmor", "bytea"},
			{"SELECT dearmor()", "dearmor", ""},
			{"SELECT pgp_armor_headers('packet','key','value')", "pgp_armor_headers", "unknown, unknown, unknown"},
			{"SELECT armor(data => 'test'::bytea)", "armor", "data => bytea"},
			{"SELECT pgp_armor_headers(data => 'packet'::text)", "pgp_armor_headers", "data => text"},
		} {
			binding(extensionMissing(tc.query, tc.name, tc.args))
		}
		multibyte := extensionMissing("SELECT 'é', armor('test'::text)", "armor", "text")
		multibyte.Diagnostic.Position = "13"
		binding(multibyte)
		comment := extensionMissing("/*é*/ SELECT armor('test'::text)", "armor", "text")
		comment.Diagnostic.Position = "14"
		binding(comment)
		for _, tc := range []struct{ query, message string }{
			{"SELECT armor(DISTINCT 'test'::bytea)", "DISTINCT specified, but armor is not an aggregate function"},
			{"SELECT armor('test'::bytea ORDER BY 1)", "ORDER BY specified, but armor is not an aggregate function"},
			{"SELECT dearmor(NULL::text) FILTER (WHERE true)", "FILTER specified, but dearmor is not an aggregate function"},
			{"SELECT pgp_armor_headers(NULL::text) OVER ()", "OVER specified, but pgp_armor_headers is not a window function nor an aggregate function"},
		} {
			binding(fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42809", Message: tc.message, Position: "8"}))
		}
	})
}
