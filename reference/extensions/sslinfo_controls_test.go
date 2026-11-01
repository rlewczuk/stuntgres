package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func sslInfoRetainedRows(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	want := sslInfoSelect("")
	if !reflect.DeepEqual(columns, want.Columns) {
		t.Fatalf("retained columns=%v want=%v", columns, want.Columns)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != len(want.Types) {
		t.Fatal("retained metadata", meta, err)
	}
	for i, col := range meta {
		if col.DatabaseTypeName() != want.Types[i] {
			t.Fatalf("retained column %d type=%s want=%s", i, col.DatabaseTypeName(), want.Types[i])
		}
	}
	if !rows.Next() {
		t.Fatal("retained row missing", rows.Err())
	}
	var used, version, cipher, cert, serial, client, issuer any
	if err := rows.Scan(&used, &version, &cipher, &cert, &serial, &client, &issuer); err != nil {
		t.Fatal(err)
	}
	got := []any{used, version, cipher, cert, serial, client, issuer}
	if !reflect.DeepEqual(got, want.Rows[0]) {
		t.Fatalf("retained values=%#v want=%#v", got, want.Rows[0])
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained tail", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceSSLInfoControls(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		sslInfoConsumers(t, p, b, c)
		sslInfoInventory(t, p, c, b.Recorder)
		q := "SELECT * FROM ssl_view"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		h := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, sslInfoSelect(q))
		if b.PG {
			// Reset/Dump/Restore are public native controls, not simulated PG SQL.
			return
		}
		old, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		schemaAt := strings.Index(dump, "CREATE SCHEMA")
		extAt := strings.Index(dump, "CREATE EXTENSION")
		tableAt := strings.Index(dump, "CREATE TABLE")
		viewAt := strings.Index(dump, "CREATE VIEW")
		if schemaAt < 0 || extAt <= schemaAt || tableAt <= extAt || viewAt <= extAt || !strings.Contains(dump, "VERSION '1.2'") {
			t.Fatal("dump must install schema and registered version before its consumers", dump)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		sslInfoRetainedRows(t, old)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='sslinfo'", []string{"extname"}, []string{"NAME"}, nil))
		sslInfoFailure(t, p, c, b.Recorder, "SELECT ssl_is_used()", sslInfoMissing("ssl_is_used()", 8))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post := func(target *sql.Conn, r *fixture.Recorder) {
			t.Helper()
			sslInfoInventory(t, p, target, r)
			for _, prepared := range []bool{false, true} {
				s := sslInfoSelect(q)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, target, r, s)
				defaults := fixture.Select("SELECT id,used,serial,version FROM ssl_saved ORDER BY id", []string{"id", "used", "serial", "version"}, []string{"INT4", "BOOL", "NUMERIC", "TEXT"}, [][]any{{int64(1), false, nil, nil}, {int64(2), false, nil, nil}, {int64(3), nil, nil, nil}})
				defaults.Prepared = prepared
				fixture.RunStep(t, p.Ctx, target, r, defaults)
			}
		}
		post(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, sslInfoSelect(q))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		beforeIDs := sslInfoIdentities(t, p, c)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nINVALID SQL;")); err == nil {
			t.Fatal("invalid replacement accepted")
		}
		if after := fixture.ExtensionDump(t, p.Ctx, p.Native); after != before {
			t.Fatal("failed restore changed committed dump")
		}
		if after := sslInfoIdentities(t, p, c); !reflect.DeepEqual(after, beforeIDs) {
			t.Fatalf("failed restore changed provider identities: %v -> %v", beforeIDs, after)
		}
		post(c, b.Recorder)
		fixture.RunStep(t, p.Ctx, h, b.Recorder, sslInfoSelect(q))
		// The shared destination helper constructs an independent NewDriver, an
		// unwrapped control pool and a middleware-recorded physical SQL borrower.
		other, target, r := pgcryptoControlDestination(t, p, "sslinfo_destination")
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		post(target, r)
		for _, dst := range []struct {
			c *sql.Conn
			r *fixture.Recorder
		}{{c, b.Recorder}, {target, r}} {
			fixture.RunStep(t, p.Ctx, dst.c, dst.r, fixture.Exec("INSERT INTO ssl_saved(id) VALUES(4)", 1))
			fixture.RunStep(t, p.Ctx, dst.c, dst.r, fixture.Select("SELECT used,serial,version FROM ssl_saved WHERE id=4", []string{"used", "serial", "version"}, []string{"BOOL", "NUMERIC", "TEXT"}, [][]any{{false, nil, nil}}))
		}
	})
}
