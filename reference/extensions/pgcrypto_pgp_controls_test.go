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

func pgpInventory(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) map[string]int64 {
	t.Helper()
	// The shared inventory asserts all twenty-six members; independently
	// recheck all eight PGP source declarations against incumbent identities.
	installed := pgcryptoRandomInventory(t, p, c, pg, "pgcrypto_owner")
	ids := make(map[string]int64, 26)
	for signature, id := range installed {
		if !strings.HasPrefix(signature, "pgcrypto_part.pgp_sym_") {
			ids[signature] = id
		}
	}
	pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")
	type declaration struct {
		result      int64
		vol, source string
	}
	want := map[string]declaration{
		"pgcrypto_part.pgp_sym_encrypt(text,text)":             {17, "v", "pgp_sym_encrypt_text"},
		"pgcrypto_part.pgp_sym_encrypt(text,text,text)":        {17, "v", "pgp_sym_encrypt_text"},
		"pgcrypto_part.pgp_sym_decrypt(bytea,text)":            {25, "i", "pgp_sym_decrypt_text"},
		"pgcrypto_part.pgp_sym_decrypt(bytea,text,text)":       {25, "i", "pgp_sym_decrypt_text"},
		"pgcrypto_part.pgp_sym_encrypt_bytea(bytea,text)":      {17, "v", "pgp_sym_encrypt_bytea"},
		"pgcrypto_part.pgp_sym_encrypt_bytea(bytea,text,text)": {17, "v", "pgp_sym_encrypt_bytea"},
		"pgcrypto_part.pgp_sym_decrypt_bytea(bytea,text)":      {17, "i", "pgp_sym_decrypt_bytea"},
		"pgcrypto_part.pgp_sym_decrypt_bytea(bytea,text,text)": {17, "i", "pgp_sym_decrypt_bytea"},
	}
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid,p.oid::regprocedure::text,prorettype,provolatile,proisstrict,proparallel,prosrc,probin,proretset,r.rolname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_roles r ON r.oid=p.proowner WHERE n.nspname='pgcrypto_part' AND proname IN ('pgp_sym_encrypt','pgp_sym_decrypt','pgp_sym_encrypt_bytea','pgp_sym_decrypt_bytea')")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, result int64
		var signature, vol, parallel, source, library, owner string
		var strict, set bool
		if err := rows.Scan(&id, &signature, &result, &vol, &strict, &parallel, &source, &library, &set, &owner); err != nil {
			t.Fatal(err)
		}
		d, ok := want[signature]
		if !ok || id <= 0 || ids[signature] != 0 || result != d.result || vol != d.vol || !strict || parallel != "s" || source != d.source || library != "$libdir/pgcrypto" || set || owner != "pgcrypto_owner" {
			t.Fatal("PGP declaration", signature, id)
		}
		for _, prior := range ids {
			if prior == id {
				t.Fatal("PGP reuses incumbent identity")
			}
		}
		ids[signature] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(ids) != 26 {
		t.Fatal("expected eighteen incumbents plus eight text/BYTEA PGP members", len(ids))
	}
	for signature, id := range ids {
		var count int
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.objid=$1 AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='pgcrypto'", id).Scan(&count); err != nil || count != 1 {
			t.Fatal("member relationship", signature, count, err)
		}
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT r.rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='pgcrypto'", []string{"rolname"}, []string{"NAME"}, [][]any{{"pgcrypto_owner"}}))
	return ids
}

func TestReferencePgcryptoPGPIdentityAndControls(t *testing.T) {
	pgpRFCAnchor(t)
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, q := range []string{"CREATE USER pgcrypto_owner SUPERUSER", "CREATE SCHEMA pgcrypto_part AUTHORIZATION pgcrypto_owner", "SET SESSION AUTHORIZATION pgcrypto_owner", "CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", "RESET SESSION AUTHORIZATION"} {
			run(fixture.Exec(q, 0))
		}
		pgpInventory(t, p, c, r, pg)
		run(fixture.Exec("CREATE TABLE pgp_packet_stored(id integer,c bytea DEFAULT pgcrypto_part.pgp_sym_encrypt('default text','foobar','s2k-count=1024'),p text GENERATED ALWAYS AS (pgcrypto_part.pgp_sym_decrypt(c,'foobar')) STORED)", 0))
		run(fixture.Exec("CREATE INDEX pgp_packet_idx ON pgp_packet_stored ((pgcrypto_part.pgp_sym_decrypt(c,'foobar')))", 0))
		run(fixture.Exec("CREATE VIEW pgp_packet_view AS SELECT id,pgcrypto_part.pgp_sym_decrypt(c,'foobar') AS decoded FROM pgp_packet_stored", 0))
		run(fixture.Exec("INSERT INTO pgp_packet_stored(id,c) VALUES(1,$1::bytea)", 1, pgpVector(t)))
		run(fixture.Exec("INSERT INTO pgp_packet_stored(id) VALUES(2)", 1))
		view := fixture.Select("SELECT id,decoded FROM pgp_packet_view ORDER BY id", []string{"id", "decoded"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "Secret message."}, {int64(2), "default text"}})
		stored := fixture.Select("SELECT p FROM pgp_packet_stored ORDER BY id", []string{"p"}, []string{"TEXT"}, [][]any{{"Secret message."}, {"default text"}})
		for _, s := range []fixture.Step{view, stored} {
			pgpBoth(t, p, c, r, s)
		}
		var encrypted []byte
		if err := c.QueryRowContext(p.Ctx, "SELECT c FROM pgp_packet_stored WHERE id=2").Scan(&encrypted); err != nil {
			t.Fatal(err)
		}
		d, err := pgpRFCDecode(encrypted, "foobar")
		if err != nil || d.text != "default text" {
			t.Fatal("stored default independent decode", err)
		}
		if pg {
			return
		} // Real native controls have no PostgreSQL counterpart.
		q := "SELECT pgcrypto_part.pgp_sym_decrypt($1::bytea,$2::text)"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		type retainedCall struct {
			query   string
			args    []any
			stmt    *sql.Stmt
			encrypt bool
		}
		calls := []retainedCall{
			{query: "SELECT pgcrypto_part.pgp_sym_encrypt($1::text,$2::text)", args: []any{"held text", "foobar"}, encrypt: true},
			{query: "SELECT pgcrypto_part.pgp_sym_encrypt($1::text,$2::text,$3::text)", args: []any{"held text", "foobar", "s2k-count=1024"}, encrypt: true},
			{query: "SELECT pgcrypto_part.pgp_sym_decrypt($1::bytea,$2::text,$3::text)", args: []any{pgpVector(t), "foobar", ""}},
		}
		for i := range calls {
			calls[i].stmt, err = c.PrepareContext(p.Ctx, calls[i].query)
			if err != nil {
				t.Fatal(err)
			}
			defer calls[i].stmt.Close()
		}
		oldConn, err := p.Memory.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer oldConn.Close()
		old, err := oldConn.QueryContext(p.Ctx, "SELECT c FROM pgp_packet_stored WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		oldMetadata := pgpMetadata(t, old, "c", "BYTEA", true, true)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		// Dump is ordinary SQL and exact ciphertext, not a serialized live key
		// or backend cipher/session handle. User-authored defaults remain SQL.
		if !strings.Contains(dump.String(), "pgp_packet_stored") || !strings.Contains(dump.String(), "pgp_sym_decrypt") {
			t.Fatal("stored consumers missing from dump")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		absent := fixture.ImplicitError(q, fixture.Diagnostic{Code: "3F000", Message: `schema "pgcrypto_part" does not exist`, Position: "8"}, pgpVector(t), "foobar")
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held, SQL: q}, r, absent)
		for _, call := range calls {
			s := fixture.ImplicitError(call.query, fixture.Diagnostic{Code: "3F000", Message: `schema "pgcrypto_part" does not exist`, Position: "8"}, call.args...)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: call.stmt, SQL: call.query}, r, s)
		}
		if !old.Next() {
			t.Fatal("held old BYTEA row lost", old.Err())
		}
		var exported []byte
		if err := old.Scan(&exported); err != nil || !bytes.Equal(exported, pgpVector(t)) {
			t.Fatal("held old BYTEA changed", err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("held row tail", old.Err())
		}
		old.Close()
		pgpColumns(t, oldMetadata, "c", "BYTEA", true, true)
		oldConn.Close()
		exported[0] ^= 255
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		pgpInventory(t, p, c, r, false)
		for _, s := range []fixture.Step{view, stored} {
			pgpBoth(t, p, c, r, s)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held, SQL: q}, r, pgpTextStep(q, "Secret message.", pgpVector(t), "foobar"))
		for _, call := range calls {
			if !call.encrypt {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: call.stmt, SQL: call.query}, r, pgpTextStep(call.query, "Secret message.", call.args...))
				continue
			}
			rows, err := call.stmt.QueryContext(p.Ctx, call.args...)
			if err != nil {
				t.Fatal(err)
			}
			pgpMetadata(t, rows, "pgp_sym_encrypt", "BYTEA", false, false)
			if !rows.Next() {
				t.Fatal("restored held encrypt row", rows.Err())
			}
			var packet []byte
			if err := rows.Scan(&packet); err != nil {
				t.Fatal(err)
			}
			d, err := pgpRFCDecode(packet, "foobar")
			if err != nil || d.text != "held text" {
				t.Fatal("restored held encrypt authentication", err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("restored held encrypt tail", rows.Err())
			}
			rows.Close()
		}
		var restored []byte
		if err := c.QueryRowContext(p.Ctx, "SELECT c FROM pgp_packet_stored WHERE id=2").Scan(&restored); err != nil || !bytes.Equal(restored, encrypted) {
			t.Fatal("restore changed ciphertext", err)
		}
		run(fixture.Exec("INSERT INTO pgp_packet_stored(id) VALUES(3)", 1))
		run(fixture.Select("SELECT decoded FROM pgp_packet_view WHERE id=3", []string{"decoded"}, []string{"TEXT"}, [][]any{{"default text"}}))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nSELECT absent_pgp_replay_function();")); err == nil {
			t.Fatal("invalid replay accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed replay changed target")
		}
		cancelCtx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(cancelCtx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled replay changed target", err)
		}
		raw, destination, recorder := pgcryptoControlDestination(t, p, "pgp_packet_missing_owner")
		fixture.RunStep(t, p.Ctx, destination, recorder, fixture.Exec("CREATE TABLE sentinel(v integer)", 0))
		sentinel := fixture.ExtensionDump(t, p.Ctx, raw)
		if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump.String())); err == nil {
			t.Fatal("missing owner replay accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, raw) != sentinel {
			t.Fatal("missing owner changed destination sentinel/identities")
		}
	})
}
