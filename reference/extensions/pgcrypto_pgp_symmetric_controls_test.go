package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// This new selected 26-member proof is deliberately independent of incumbent
// 22-member expectations, which migrate only after the tests-first checkpoint.
func symInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, schema string) map[string]int64 {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO public", 0))
	type declaration struct {
		signature, source, vol string
		result                 int64
		strict, set            bool
	}
	declarations := []declaration{
		{"digest(text,text)", "pg_digest", "i", 17, true, false}, {"digest(bytea,text)", "pg_digest", "i", 17, true, false},
		{"hmac(text,text,text)", "pg_hmac", "i", 17, true, false}, {"hmac(bytea,bytea,text)", "pg_hmac", "i", 17, true, false},
		{"crypt(text,text)", "pg_crypt", "i", 25, true, false}, {"gen_salt(text)", "pg_gen_salt", "v", 25, true, false}, {"gen_salt(text,integer)", "pg_gen_salt_rounds", "v", 25, true, false},
		{"encrypt(bytea,bytea,text)", "pg_encrypt", "i", 17, true, false}, {"decrypt(bytea,bytea,text)", "pg_decrypt", "i", 17, true, false},
		{"encrypt_iv(bytea,bytea,bytea,text)", "pg_encrypt_iv", "i", 17, true, false}, {"decrypt_iv(bytea,bytea,bytea,text)", "pg_decrypt_iv", "i", 17, true, false},
		{"gen_random_bytes(integer)", "pg_random_bytes", "v", 17, true, false}, {"gen_random_uuid()", "pg_random_uuid", "v", 2950, false, false}, {"fips_mode()", "pg_check_fipsmode", "v", 16, true, false},
		{"armor(bytea)", "pg_armor", "i", 25, true, false}, {"armor(bytea,text[],text[])", "pg_armor", "i", 25, true, false},
		{"dearmor(text)", "pg_dearmor", "i", 17, true, false}, {"pgp_armor_headers(text)", "pgp_armor_headers", "i", 2249, true, true},
		{"pgp_sym_encrypt(text,text)", "pgp_sym_encrypt_text", "v", 17, true, false}, {"pgp_sym_encrypt(text,text,text)", "pgp_sym_encrypt_text", "v", 17, true, false},
		{"pgp_sym_decrypt(bytea,text)", "pgp_sym_decrypt_text", "i", 25, true, false}, {"pgp_sym_decrypt(bytea,text,text)", "pgp_sym_decrypt_text", "i", 25, true, false},
		{"pgp_sym_encrypt_bytea(bytea,text)", "pgp_sym_encrypt_bytea", "v", 17, true, false}, {"pgp_sym_encrypt_bytea(bytea,text,text)", "pgp_sym_encrypt_bytea", "v", 17, true, false},
		{"pgp_sym_decrypt_bytea(bytea,text)", "pgp_sym_decrypt_bytea", "i", 17, true, false}, {"pgp_sym_decrypt_bytea(bytea,text,text)", "pgp_sym_decrypt_bytea", "i", 17, true, false},
	}
	want := make(map[string]declaration, 26)
	for _, d := range declarations {
		want[schema+"."+d.signature] = d
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.oid::regprocedure::text,prorettype,provolatile,proisstrict,proparallel,prosrc,probin,proretset,r.rolname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname=$1 AND proname IN ('digest','hmac','crypt','gen_salt','encrypt','decrypt','encrypt_iv','decrypt_iv','gen_random_bytes','gen_random_uuid','fips_mode','armor','dearmor','pgp_armor_headers','pgp_sym_encrypt','pgp_sym_decrypt','pgp_sym_encrypt_bytea','pgp_sym_decrypt_bytea')", schema)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]int64{}
	used := map[int64]bool{}
	for rows.Next() {
		var id, result int64
		var signature, vol, parallel, source, library, owner string
		var strict, set bool
		if err := rows.Scan(&id, &signature, &result, &vol, &strict, &parallel, &source, &library, &set, &owner); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(signature, schema+".") {
			signature = schema + "." + signature
		}
		d, ok := want[signature]
		if !ok || ids[signature] != 0 || id <= 0 || used[id] || result != d.result || vol != d.vol || strict != d.strict || set != d.set || parallel != "s" || source != d.source || library != "$libdir/pgcrypto" || owner != "pgcrypto_owner" {
			t.Fatal("selected 26-member declaration", signature, id, result, vol, source, owner)
		}
		ids[signature] = id
		used[id] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(ids) != 26 {
		t.Fatal("complete selected 26-member bag", len(ids))
	}
	for signature, id := range ids {
		var n int
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_roles r ON r.oid=e.extowner WHERE d.classid=1255 AND d.objid=$1 AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='pgcrypto' AND r.rolname='pgcrypto_owner'", id).Scan(&n); err != nil || n != 1 {
			t.Fatal("selected source/owner/member link", signature, n, err)
		}
	}
	// Slot17's OUT/set metadata is part of the incumbent contract even when
	// public relocation makes regprocedure names unqualified.
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT proallargtypes::text,proargmodes::text,proargnames::text,prorows::bigint FROM pg_proc WHERE oid=$1", []string{"proallargtypes", "proargmodes", "proargnames", "prorows"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{"{25,25,25}", "{i,o,o}", "{\"\",key,value}", int64(1000)}}, ids[schema+".pgp_armor_headers(text)"]))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1 AND proname IN ('armor','dearmor') AND proallargtypes IS NULL AND proargmodes IS NULL AND proargnames IS NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}, schema))
	if !pg {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pgcrypto'", []string{"count"}, []string{"INT8"}, [][]any{{int64(26)}}))
	}
	return ids
}

func TestReferencePgcryptoPGPSymmetricControls(t *testing.T) {
	symAnchor(t)
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE USER pgcrypto_owner SUPERUSER", "CREATE SCHEMA pgcrypto_part AUTHORIZATION pgcrypto_owner", "SET SESSION AUTHORIZATION pgcrypto_owner", "CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", "RESET SESSION AUTHORIZATION"} {
			run(fixture.Exec(q, 0))
		}
		initialIDs := symInventory(t, p, c, r, pg, "pgcrypto_part")
		options := "cipher-algo=aes256,s2k-digest-algo=sha256,s2k-count=1024,compress-algo=2,sess-key=1,s2k-cipher-algo=aes128"
		run(fixture.Exec("CREATE TABLE sym_stored(id integer,c bytea DEFAULT pgcrypto_part.pgp_sym_encrypt_bytea('\\x000aff'::bytea,'stored-pass','"+options+"'),p bytea GENERATED ALWAYS AS (pgcrypto_part.pgp_sym_decrypt_bytea(c,'stored-pass')) STORED)", 0))
		run(fixture.Exec("CREATE INDEX sym_idx ON sym_stored ((pgcrypto_part.pgp_sym_decrypt_bytea(c,'stored-pass')))", 0))
		run(fixture.Exec("CREATE VIEW sym_view AS SELECT id,pgcrypto_part.pgp_sym_decrypt_bytea(c,'stored-pass') AS decoded FROM sym_stored", 0))
		run(fixture.Exec("CREATE TABLE sym_text(c bytea DEFAULT pgcrypto_part.pgp_sym_encrypt('text retained','stored-pass','cipher-algo=cast5,s2k-count=1024'),p text GENERATED ALWAYS AS (pgcrypto_part.pgp_sym_decrypt(c,'stored-pass')) STORED)", 0))
		run(fixture.Exec("INSERT INTO sym_text DEFAULT VALUES", 1))
		run(fixture.Exec("INSERT INTO sym_stored(id) VALUES(1)", 1))
		noMDC := symProfile{cipher: 4, wrap: 4, digest: 1, mode: 1, noMDC: true}
		second := symBuild("stored-pass", pgpRFCLiteral("no MDC retained", "", 'b'), noMDC, "")
		run(fixture.Exec("INSERT INTO sym_stored(id,c) VALUES(2,$1::bytea)", 1, second))
		view := fixture.Select("SELECT id,decoded FROM sym_view ORDER BY id", []string{"id", "decoded"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), string([]byte{0, 10, 255})}, {int64(2), "no MDC retained"}})
		stored := fixture.Select("SELECT p FROM sym_stored ORDER BY id", []string{"p"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 10, 255})}, {"no MDC retained"}})
		text := fixture.Select("SELECT p FROM sym_text", []string{"p"}, []string{"TEXT"}, [][]any{{"text retained"}})
		for _, s := range []fixture.Step{view, stored, text} {
			pgpBoth(t, p, c, r, s)
		}
		var ciphertext []byte
		if err := c.QueryRowContext(p.Ctx, "SELECT c FROM sym_stored WHERE id=1").Scan(&ciphertext); err != nil {
			t.Fatal(err)
		}
		d, err := symDecode(ciphertext, "stored-pass")
		if err != nil || !bytes.Equal(d.data, []byte{0, 10, 255}) || d.profile.cipher != 9 || d.profile.wrap != 7 || d.profile.digest != 8 || !d.profile.session || d.profile.compression != 2 {
			t.Fatal("stored default independent semantics", err)
		}
		// Relocation migrates real recipes, not just names in pg_proc. Every
		// incumbent/member identity remains itself, without allocator-number goldens.
		run(fixture.Exec("ALTER EXTENSION pgcrypto SET SCHEMA public", 0))
		relocated := symInventory(t, p, c, r, pg, "public")
		for signature, id := range initialIDs {
			if relocated["public."+strings.TrimPrefix(signature, "pgcrypto_part.")] != id {
				t.Fatal("relocation changed selected member identity", signature)
			}
		}
		for _, s := range []fixture.Step{view, stored, text} {
			pgpBoth(t, p, c, r, s)
		}
		pgpBoth(t, p, c, r, symByteStep("SELECT public.pgp_sym_decrypt_bytea($1::bytea,$2::text)", []byte{0, 10, 255}, ciphertext, "stored-pass"))
		run(fixture.Exec("ALTER EXTENSION pgcrypto SET SCHEMA pgcrypto_part", 0))
		if pg {
			return
		} // The remaining real controls have no PostgreSQL equivalent.
		type call struct {
			q, name, typ    string
			args            []any
			encrypt, binary bool
		}
		calls := []call{
			{"SELECT pgcrypto_part.pgp_sym_encrypt($1::text,$2::text)", "pgp_sym_encrypt", "BYTEA", []any{"held text", "stored-pass"}, true, false},
			{"SELECT pgcrypto_part.pgp_sym_encrypt($1::text,$2::text,$3::text)", "pgp_sym_encrypt", "BYTEA", []any{"held text", "stored-pass", "cipher-algo=3des,s2k-count=1024"}, true, false},
			{"SELECT pgcrypto_part.pgp_sym_encrypt_bytea($1::bytea,$2::text)", "pgp_sym_encrypt_bytea", "BYTEA", []any{[]byte{0, 255}, "stored-pass"}, true, true},
			{"SELECT pgcrypto_part.pgp_sym_encrypt_bytea($1::bytea,$2::text,$3::text)", "pgp_sym_encrypt_bytea", "BYTEA", []any{[]byte{0, 255}, "stored-pass", options}, true, true},
			{"SELECT pgcrypto_part.pgp_sym_decrypt($1::bytea,$2::text)", "pgp_sym_decrypt", "TEXT", []any{pgpVector(t), "foobar"}, false, false},
			{"SELECT pgcrypto_part.pgp_sym_decrypt($1::bytea,$2::text,$3::text)", "pgp_sym_decrypt", "TEXT", []any{pgpVector(t), "foobar", ""}, false, false},
			{"SELECT pgcrypto_part.pgp_sym_decrypt_bytea($1::bytea,$2::text)", "pgp_sym_decrypt_bytea", "BYTEA", []any{ciphertext, "stored-pass"}, false, true},
			{"SELECT pgcrypto_part.pgp_sym_decrypt_bytea($1::bytea,$2::text,$3::text)", "pgp_sym_decrypt_bytea", "BYTEA", []any{second, "stored-pass", ""}, false, true},
		}
		held := make([]*sql.Stmt, len(calls))
		for i, call := range calls {
			held[i], err = c.PrepareContext(p.Ctx, call.q)
			if err != nil {
				t.Fatal(err)
			}
			defer held[i].Close()
		}
		oldConn, err := p.Memory.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer oldConn.Close()
		old, err := oldConn.QueryContext(p.Ctx, "SELECT c FROM sym_stored WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata := pgpMetadata(t, old, "c", "BYTEA", true, true)
		columns, err := old.Columns()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "pgp_sym_encrypt_bytea") || !strings.Contains(dump.String(), "pgp_sym_decrypt_bytea") {
			t.Fatal("stored BYTEA recipes missing from dump")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for i, call := range calls {
			s := fixture.ImplicitError(call.q, fixture.Diagnostic{Code: "3F000", Message: `schema "pgcrypto_part" does not exist`, Position: "8"}, call.args...)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held[i], SQL: call.q}, r, s)
		}
		if !old.Next() {
			t.Fatal("held pre-reset row", old.Err())
		}
		var snapshot []byte
		if err := old.Scan(&snapshot); err != nil || !bytes.Equal(snapshot, ciphertext) {
			t.Fatal("pre-reset ciphertext snapshot", err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("old row EOF", old.Err())
		}
		old.Close()
		oldConn.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		symInventory(t, p, c, r, false, "pgcrypto_part")
		if !bytes.Equal(snapshot, ciphertext) || len(columns) != 1 || columns[0] != "c" {
			t.Fatal("restore mutated caller snapshots")
		}
		pgpColumns(t, metadata, "c", "BYTEA", true, true)
		for _, s := range []fixture.Step{view, stored, text} {
			pgpBoth(t, p, c, r, s)
		}
		for i, call := range calls {
			rows, err := held[i].QueryContext(p.Ctx, call.args...)
			if err != nil {
				t.Fatal("held rebind", err)
			}
			pgpMetadata(t, rows, call.name, call.typ, false, false)
			if !rows.Next() {
				t.Fatal("held rebind row", rows.Err())
			}
			var value []byte
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			want := []byte("Secret message.")
			if call.encrypt {
				decoded, err := symDecode(value, "stored-pass")
				if err != nil {
					t.Fatal("held encryption decode", err)
				}
				value = decoded.data
				want = []byte("held text")
				if call.binary {
					want = []byte{0, 255}
				}
			} else if call.binary {
				want = []byte{0, 10, 255}
				if len(call.args) == 3 {
					want = []byte("no MDC retained")
				}
			}
			if !bytes.Equal(value, want) {
				t.Fatal("held rebind payload")
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("held rebind EOF", rows.Err())
			}
			rows.Close()
		}
		for _, tc := range []struct {
			id     int
			packet []byte
		}{{1, ciphertext}, {2, second}} {
			var got []byte
			if err := c.QueryRowContext(p.Ctx, "SELECT c FROM sym_stored WHERE id=$1::integer", tc.id).Scan(&got); err != nil || !bytes.Equal(got, tc.packet) {
				t.Fatal("restore changed exact ciphertext", tc.id, err)
			}
		}
		snapshot[0] ^= 255
		run(fixture.Exec("INSERT INTO sym_stored(id) VALUES(3)", 1))
		run(fixture.Select("SELECT decoded FROM sym_view WHERE id=3", []string{"decoded"}, []string{"BYTEA"}, [][]any{{string([]byte{0, 10, 255})}}))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nSELECT absent_symmetric_replay();")); err == nil {
			t.Fatal("invalid replay accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed replay mutated source destination")
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled replay mutated destination", err)
		}
		raw, destination, recorder := pgcryptoControlDestination(t, p, "symmetric_missing_owner")
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Exec("CREATE TABLE sentinel(v integer)", 0))
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Exec("INSERT INTO sentinel VALUES(47)", 1))
		sentinel := fixture.ExtensionDump(t, p.Ctx, raw)
		if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump.String())); err == nil {
			t.Fatal("missing-owner replay accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, raw) != sentinel {
			t.Fatal("failed replay mutated independent destination sentinel")
		}
		ctx, cancel = context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, raw, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, raw) != sentinel {
			t.Fatal("canceled replay mutated independent sentinel", err)
		}
		// Supply the real owner so this failure occurs after replaying the
		// selected provider and every stored consumer, not at an early conflict.
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Exec("CREATE USER pgcrypto_owner SUPERUSER", 0))
		sentinel = fixture.ExtensionDump(t, p.Ctx, raw)
		if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump.String()+"\nSELECT absent_symmetric_replay();")); err == nil {
			t.Fatal("late invalid replay accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, raw) != sentinel {
			t.Fatal("late replay failure published provider/consumers or changed sentinel")
		}
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(47)}}))
	})
}
