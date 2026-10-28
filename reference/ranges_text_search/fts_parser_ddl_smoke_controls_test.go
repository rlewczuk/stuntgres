package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Native controls have no SQL equivalent. Replay their exact emitted bytes on
// the adopted oracle, then leave public clean for the shared SQL backend phase.
func parserDDLSmokeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	if b.PG {
		return
	}
	check := func(p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
		parserDDLCheck(t, p, b, e, "public.parser_ddl_smoke_new", "public.parser_ddl_smoke_cfg")
		fixture.RunStep(t, p.Ctx, e, b.Recorder, parserDDLCallbacks("parser_ddl_smoke_new", 0))
	}
	var output bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &output); err != nil {
		t.Fatal(err)
	}
	stream := bytes.Clone(output.Bytes())
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream)); err != nil {
		t.Fatal(err)
	}
	check(p, b, c)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream)); err != nil {
		t.Fatal(err)
	}
	check(p, b, c)
	fresh := fixture.NewFixture(t, false, true)
	if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(stream)); err != nil {
		t.Fatal(err)
	}
	check(fresh, fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}, fresh.Memory)
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(stream)); err != nil {
			t.Fatal("actual smoke dump replay", err)
		}
		check(p, fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}, p.Postgres)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

// Public schema cleanup cannot repair a renamed bootstrap parser. Resolve its
// actual identity so even a failure between RENAME and SET SCHEMA is repaired.
func parserDDLRestoreDefault(t *testing.T, conn *sql.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var namespace, name string
	if err := conn.QueryRowContext(ctx, "SELECT n.nspname,p.prsname FROM pg_ts_parser p JOIN pg_namespace n ON n.oid=p.prsnamespace WHERE p.oid=3722").Scan(&namespace, &name); err != nil {
		t.Error("resolve bootstrap parser cleanup", err)
		return
	}
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	current := quote(namespace) + "." + quote(name)
	if namespace != "pg_catalog" {
		if _, err := conn.ExecContext(ctx, "ALTER TEXT SEARCH PARSER "+current+" SET SCHEMA pg_catalog"); err != nil {
			t.Error("restore bootstrap parser namespace", err)
			return
		}
		current = "pg_catalog." + quote(name)
	}
	if name != "default" {
		if _, err := conn.ExecContext(ctx, "ALTER TEXT SEARCH PARSER "+current+" RENAME TO "+quote("default")); err != nil {
			t.Error("restore bootstrap parser name", err)
		}
	}
}
