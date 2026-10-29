package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLevenshteinControls(t *testing.T) {
	levenshteinCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE USER lev_owner SUPERUSER", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION lev_owner", 0))
		check := levenshteinStored(t, p, b, c)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		levenshteinInventory(t, p, b, c, "lev_owner")
		if b.PG {
			return // Only the shared SQL phase is exercised by oracle discovery.
		}
		dump := extensionDump(t, p.Ctx, p.Native)
		q := "SELECT lev_addon.levenshtein_less_equal($1,$2,$3,$4,$5,$6)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		heldStatement := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		frame := levenshteinSelect(q, "levenshtein_less_equal", int64(5), "a", "b", 2, 3, 7, 5)
		fixture.RunStep(t, p.Ctx, heldStatement, b.Recorder, frame)
		retained, err := c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		levenshteinControlMetadata(t, retained, check)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='lev_source' OR relname='lev_values'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, heldStatement, b.Recorder, frame)
		levenshteinControlRows(t, retained, check)
		run(check)
		run(fixture.Prepared(check))
		levenshteinInventory(t, p, b, c, "lev_owner")
		extensionControlsFailures(t, p, c, b.Recorder, "fuzzystrmatch", dump, check)
		ioFailure := errors.New("levenshtein restore reader failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, fixture.CatalogControlFailingReader{Err: ioFailure}); !errors.Is(err, ioFailure) {
			t.Fatal("reader error", err)
		}
		run(check)
		ctx, cancel := context.WithCancel(p.Ctx)
		err = stuntgres.Restore(ctx, p.Native, &daitchRestoreCancelReader{cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("staged-prefix cancellation", err)
		}
		run(check)
		bad := "CREATE SCHEMA lev_addon; CREATE EXTENSION fuzzystrmatch SCHEMA lev_addon VERSION 'missing';"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("bad staged provider accepted")
		}
		run(check)
		other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "lev_destination")
		if _, err := other.ExecContext(p.Ctx, "CREATE USER lev_owner NOSUPERUSER"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		otherConn, err := other.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer otherConn.Close()
		// The independent pool is unwrapped: assert actual public rows/metadata,
		// without fabricating middleware recorder provenance.
		rows, err := otherConn.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		levenshteinControlMetadata(t, rows, check)
		levenshteinControlRows(t, rows, check)
		if got := extensionDump(t, p.Ctx, other); got != dump {
			t.Fatal("independent symbolic redump changed")
		}
		var owner string
		if err := otherConn.QueryRowContext(p.Ctx, "SELECT rolname FROM pg_roles WHERE oid=(SELECT extowner FROM pg_extension WHERE extname='fuzzystrmatch')").Scan(&owner); err != nil || owner != "lev_owner" {
			t.Fatal("independent extension owner", owner, err)
		}
		var wrong int64
		if err := otherConn.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE (proname='levenshtein' OR proname='levenshtein_less_equal') AND rolname<>'lev_owner'").Scan(&wrong); err != nil || wrong != 0 {
			t.Fatal("independent member owners", wrong, err)
		}
		missing := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "lev_missing_owner")
		if _, err := missing.ExecContext(p.Ctx, "CREATE TABLE lev_sentinel(v int); INSERT INTO lev_sentinel VALUES(77)"); err != nil {
			t.Fatal(err)
		}
		before := extensionDump(t, p.Ctx, missing)
		if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(dump)); err == nil {
			t.Fatal("missing named owner accepted")
		}
		if extensionDump(t, p.Ctx, missing) != before {
			t.Fatal("missing owner changed target")
		}
		run(fixture.Exec("UPDATE lev_source SET dst='a' WHERE id=2", 1))
		run(fixture.Select("SELECT g,bounded FROM lev_values WHERE id=2", []string{"g", "bounded"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}}))
		run(fixture.Exec("INSERT INTO lev_source(id,src,dst) VALUES(3,'a','b')", 1))
		run(fixture.Select("SELECT d,g,bounded FROM lev_values WHERE id=3", []string{"d", "g", "bounded"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(3), int64(4), int64(4)}}))
	})
}

func levenshteinControlMetadata(t *testing.T, rows *sql.Rows, check fixture.Step) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, check.Columns) {
		t.Fatal("control columns", columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(check.Types) {
		t.Fatal("control metadata", types, err)
	}
	for i, typ := range types {
		if typ.DatabaseTypeName() != check.Types[i] || typ.ScanType() != reflect.TypeFor[int64]() {
			t.Fatal("control INT4 metadata", i, typ)
		}
	}
}

func levenshteinControlRows(t *testing.T, rows *sql.Rows, check fixture.Step) {
	t.Helper()
	defer rows.Close()
	for i, want := range check.Rows {
		if !rows.Next() {
			t.Fatal("control missing row", i, rows.Err())
		}
		values := make([]any, len(want))
		dest := make([]any, len(want))
		for j := range values {
			dest[j] = &values[j]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(values, want) {
			t.Fatal("control values", values, want)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("control EOF", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
