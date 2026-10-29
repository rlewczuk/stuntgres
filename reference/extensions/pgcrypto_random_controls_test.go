package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgcryptoRandomConsumerSetup(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE TABLE pgcrypto_random_stored(id integer,b bytea DEFAULT pgcrypto_part.gen_random_bytes(32),u uuid DEFAULT pgcrypto_part.gen_random_uuid(),f boolean DEFAULT pgcrypto_part.fips_mode())", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_random_view AS SELECT id,pgcrypto_part.gen_random_bytes(32) AS b,pgcrypto_part.gen_random_uuid() AS u,pgcrypto_part.fips_mode() AS f FROM pgcrypto_random_stored", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_random_nested AS SELECT id,(SELECT pgcrypto_part.gen_random_bytes(32)) AS b,(SELECT pgcrypto_part.gen_random_uuid()) AS u,(SELECT pgcrypto_part.fips_mode()) AS f FROM pgcrypto_random_stored", 0))
	run(fixture.Exec("INSERT INTO pgcrypto_random_stored(id) VALUES(1),(2)", 2))
}

// Captured random octets are used only to prove same-row preservation, not as
// expectations for fresh generation or comparison against the other backend.
func pgcryptoRandomStored(t *testing.T, p *fixture.Pair, c *sql.Conn, pg bool) fixture.Step {
	t.Helper()
	q := "SELECT id,b,u,f FROM pgcrypto_random_stored ORDER BY id"
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 4 {
		t.Fatal(meta, err)
	}
	for i, name := range []string{"INT4", "BYTEA", "UUID", "BOOL"} {
		if meta[i].DatabaseTypeName() != name {
			t.Fatal("stored type", i, meta[i].DatabaseTypeName())
		}
	}
	var expected [][]any
	for rows.Next() {
		var id int64
		var b, u, f any
		if err := rows.Scan(&id, &b, &u, &f); err != nil {
			t.Fatal(err)
		}
		binary, ok := b.([]byte)
		if !ok || len(binary) != 32 {
			t.Fatalf("stored BYTEA %T %v", b, b)
		}
		uuid := pgcryptoRandomUUIDShape(t, u, pg)
		mode, ok := f.(bool)
		if !ok || mode != pgcryptoRandomFIPSExpected(pg) {
			t.Fatal("stored FIPS", f)
		}
		expected = append(expected, []any{id, string(binary), uuid, mode})
	}
	if rows.Err() != nil || len(expected) != 2 || expected[0][0] != int64(1) || expected[1][0] != int64(2) {
		t.Fatal("stored rows", expected, rows.Err())
	}
	return fixture.Select(q, []string{"id", "b", "u", "f"}, []string{"INT4", "BYTEA", "UUID", "BOOL"}, expected)
}

func pgcryptoRandomFresh(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	for _, relation := range []string{"pgcrypto_random_view", "pgcrypto_random_nested"} {
		var firstB, firstU string
		for i := range 3 {
			rows, err := c.QueryContext(p.Ctx, "SELECT b,u,f FROM "+relation+" WHERE id=1")
			if err != nil {
				t.Fatal(err)
			}
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 3 {
				t.Fatal(meta, err)
			}
			for j, name := range []string{"BYTEA", "UUID", "BOOL"} {
				if meta[j].DatabaseTypeName() != name {
					t.Fatal("recipe type", name)
				}
			}
			if !rows.Next() {
				t.Fatal(rows.Err())
			}
			var b, u, f any
			if err := rows.Scan(&b, &u, &f); err != nil {
				t.Fatal(err)
			}
			binary, ok := b.([]byte)
			if !ok || len(binary) != 32 {
				t.Fatalf("view BYTEA %T", b)
			}
			uuid := pgcryptoRandomUUIDShape(t, u, pg)
			mode, ok := f.(bool)
			if !ok || mode != pgcryptoRandomFIPSExpected(pg) {
				t.Fatal(f)
			}
			if i == 0 {
				firstB = string(binary)
				firstU = uuid
			} else if firstB == string(binary) || firstU == uuid {
				t.Fatal("volatile recipe reused random result", relation)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal(rows.Err())
			}
			rows.Close()
		}
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT id,octet_length(b) AS size,f FROM pgcrypto_random_stored ORDER BY id", []string{"id", "size", "f"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(1), int64(32), pgcryptoRandomFIPSExpected(pg)}, {int64(2), int64(32), pgcryptoRandomFIPSExpected(pg)}}))
}

func pgcryptoRandomDependencies(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, ids map[string]int64) {
	t.Helper()
	for _, v := range []struct {
		query, signature string
		class            int64
	}{
		{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_random_stored'::regclass::oid AND adnum=2", "pgcrypto_part.gen_random_bytes(integer)", 2604},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_random_stored'::regclass::oid AND adnum=3", "pgcrypto_part.gen_random_uuid()", 2604},
		{"SELECT oid FROM pg_attrdef WHERE adrelid='pgcrypto_random_stored'::regclass::oid AND adnum=4", "pgcrypto_part.fips_mode()", 2604},
	} {
		var id int64
		if err := c.QueryRowContext(p.Ctx, v.query).Scan(&id); err != nil || id <= 0 {
			t.Fatal(id, err)
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT refobjid::bigint AS refobjid FROM pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=1255 AND deptype='n'", []string{"refobjid"}, []string{"INT8"}, [][]any{{ids[v.signature]}}, v.class, id))
	}
	for _, name := range []string{"pgcrypto_random_view", "pgcrypto_random_nested"} {
		var id int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_rewrite WHERE ev_class=$1::regclass::oid AND rulename='_RETURN'", name).Scan(&id); err != nil || id <= 0 {
			t.Fatal(id, err)
		}
		s := fixture.Select("SELECT refobjid::bigint AS refobjid FROM pg_depend WHERE classid=2618 AND objid=$1 AND refclassid=1255 AND deptype='n'", []string{"refobjid"}, []string{"INT8"}, [][]any{{ids["pgcrypto_part.gen_random_bytes(integer)"]}, {ids["pgcrypto_part.gen_random_uuid()"]}, {ids["pgcrypto_part.fips_mode()"]}}, id)
		s.Bag = true
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
}

func pgcryptoRandomVolatility(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	for i, v := range []struct{ typ, call string }{{"bytea", "gen_random_bytes(1)"}, {"uuid", "gen_random_uuid()"}, {"boolean", "fips_mode()"}} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError(fmt.Sprintf("CREATE TABLE pgcrypto_random_volatile_%d(v %s GENERATED ALWAYS AS (pgcrypto_part.%s) STORED)", i, v.typ, v.call), fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError(fmt.Sprintf("CREATE INDEX pgcrypto_random_volatile_idx_%d ON pgcrypto_random_stored ((pgcrypto_part.%s))", i, v.call), fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"}))
	}
}

func pgcryptoRandomRejectReplay(t *testing.T, p *fixture.Pair, db *sql.DB, script string, post func()) {
	t.Helper()
	before := extensionDump(t, p.Ctx, db)
	for _, bad := range []string{script + "\nINVALID SQL;", strings.Replace(script, "VERSION '1.4'", "VERSION 'missing'", 1), strings.Replace(script, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1)} {
		if bad == script {
			t.Fatal("missing replay corruption target")
		}
		if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid replay accepted")
		}
		if extensionDump(t, p.Ctx, db) != before {
			t.Fatal("failed replay changed target")
		}
		post()
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err := stuntgres.Restore(ctx, db, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || extensionDump(t, p.Ctx, db) != before {
		t.Fatal("cancelled replay changed target", err)
	}
	post()
}

func pgcryptoRandomConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	for _, q := range []string{"CREATE USER pgcrypto_owner SUPERUSER", "CREATE SCHEMA pgcrypto_part AUTHORIZATION pgcrypto_owner", "SET SESSION AUTHORIZATION pgcrypto_owner", "CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", "RESET SESSION AUTHORIZATION"} {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
	}
	pgcryptoRandomConsumerSetup(t, p, c, r)
	stored := pgcryptoRandomStored(t, p, c, pg)
	owner := "pgcrypto_owner"
	ids := pgcryptoRandomInventory(t, p, c, pg, owner)
	pgcryptoRandomDependencies(t, p, c, r, ids)
	pgcryptoRandomFresh(t, p, c, r, pg)
	pgcryptoRandomVolatility(t, p, c, r)
	if pg {
		return
	}
	q := "SELECT pgcrypto_part.gen_random_uuid() AS value"
	held, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	old, err := c.QueryContext(p.Ctx, stored.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	script := extensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for i, want := range stored.Rows {
		if !old.Next() {
			t.Fatal("old row missing", i, old.Err())
		}
		var id int64
		var b []byte
		var u string
		var f bool
		if err := old.Scan(&id, &b, &u, &f); err != nil || !reflect.DeepEqual([]any{id, string(b), u, f}, want) {
			t.Fatal("old stored row changed", i, err)
		}
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
	old.Close()
	for i, name := range stored.Types {
		if meta[i].DatabaseTypeName() != name {
			t.Fatal("old metadata", name)
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	post := func(conn *sql.Conn, record *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, record, stored)
		newIDs := pgcryptoRandomInventory(t, p, conn, false, owner)
		pgcryptoRandomDependencies(t, p, conn, record, newIDs)
		pgcryptoRandomFresh(t, p, conn, record, false)
		pgcryptoRandomVolatility(t, p, conn, record)
	}
	post(c, r)
	rows, err := held.QueryContext(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	pgcryptoRandomUUIDRows(t, rows, false)
	pgcryptoRandomRejectReplay(t, p, p.Native, script, func() { post(c, r) })
	other, target, record := pgcryptoControlDestination(t, p, "pgcrypto_random_independent")
	if _, err := other.ExecContext(p.Ctx, "CREATE USER pgcrypto_owner NOSUPERUSER"); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	post(target, record)
	missing, missingConn, missingRecord := pgcryptoControlDestination(t, p, "pgcrypto_random_missing_owner")
	fixture.RunStep(t, p.Ctx, missingConn, missingRecord, fixture.Exec("CREATE TABLE pgcrypto_random_sentinel(v integer)", 0))
	before := extensionDump(t, p.Ctx, missing)
	if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(script)); err == nil {
		t.Fatal("missing named owner accepted")
	}
	if extensionDump(t, p.Ctx, missing) != before {
		t.Fatal("missing owner changed destination")
	}
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_random_stored(id) VALUES(3)", 1))
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT octet_length(b) AS size,f FROM pgcrypto_random_stored WHERE id=3", []string{"size", "f"}, []string{"INT4", "BOOL"}, [][]any{{int64(32), pgcryptoRandomFIPSExpected(false)}}))
	pgcryptoRandomUUIDQuery(t, p, c, "SELECT u AS value FROM pgcrypto_random_stored WHERE id=3", false)
}

func TestReferencePgcryptoRandomConsumersAndControls(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoRandomConsumers(t, p, c, r, pg)
	})
}

func TestReferencePgcryptoRandomFeatureSmoke(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoRandomConsumers(t, p, c, r, pg)
		pgcryptoRandomBytes(t, p, c, r)
		fixture.RunStep(t, p.Ctx, c, r, pgcryptoRandomRange("SELECT pgcrypto_part.gen_random_bytes(0)"))
		pgcryptoRandomUUIDBindings(t, p, c, r, pg)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO public", 0))
		pgcryptoRandomFIPS(t, p, c, r, pg)
	})
}
