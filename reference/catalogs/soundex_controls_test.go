package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSoundexControls(t *testing.T) {
	soundexCase(t, soundexControls)
}

func soundexControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	check := soundexStored(t, p, b, c)
	run(fixture.Exec("CREATE TABLE sx_sentinel(id int)", 0))
	run(fixture.Exec("INSERT INTO sx_sentinel VALUES(17)", 1))
	sentinel := fixture.Select("SELECT id FROM sx_sentinel ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}})
	soundexInventory(t, p, b, c)
	if b.PG {
		return // Reset/Dump/Restore are native APIs, not PostgreSQL facilities.
	}
	dump := extensionDump(t, p.Ctx, p.Native)
	q := "SELECT sx_addon.soundex($1),sx_addon.difference($1,$2)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	frame := fixture.Select(q, []string{"soundex", "difference"}, []string{"TEXT", "INT4"}, [][]any{{"A500", int64(4)}}, "Ann", "Anne")
	fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
	type retainedResult struct {
		rows    *sql.Rows
		step    fixture.Step
		columns []string
		types   []*sql.ColumnType
	}
	var retained []retainedResult
	for _, name := range []string{"soundex", "difference"} {
		call := "sx_addon.soundex(name)"
		values := [][]any{{"A500"}, {"A500"}, {"A536"}, {nil}}
		if name == "difference" {
			call = "sx_addon.difference(name,'Ann')"
			values = [][]any{{int64(4)}, {int64(4)}, {int64(2)}, {nil}}
		}
		s := soundexSelect(b, "SELECT "+call+" FROM sx_source ORDER BY id", name, nil)
		s.Rows = values
		rows, err := c.QueryContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		s.CheckMetadata(t, rows)
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		retained = append(retained, retainedResult{rows, s, columns, types})
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='fuzzystrmatch'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	missing := extensionMissing("SELECT soundex('Ann')", "soundex", "unknown")
	missing.SelectRows, missing.ErrorStage = true, "execute"
	run(missing)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
	for _, old := range retained {
		old.step.CheckMetadata(t, old.rows)
		columns, err := old.rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, old.columns) || !reflect.DeepEqual(old.columns, old.step.Columns) {
			t.Fatal("detached columns", columns, old.columns, err)
		}
		// Detached descriptors remain meaningful, not descriptors of the new catalog.
		if len(old.types) != 1 || old.types[0].Name() != old.step.Columns[0] || old.types[0].DatabaseTypeName() != old.step.Types[0] {
			t.Fatal("detached descriptors", old.types)
		}
		meta := old.types[0]
		wantScan, wantLength, wantLengthKnown := reflect.TypeFor[string](), int64(9223372036854775807), true
		if old.step.Types[0] == "INT4" {
			wantScan, wantLength, wantLengthKnown = reflect.TypeFor[int64](), 0, false
		}
		length, lengthKnown := meta.Length()
		precision, scale, decimalKnown := meta.DecimalSize()
		nullable, nullableKnown := meta.Nullable()
		if meta.ScanType() != wantScan || length != wantLength || lengthKnown != wantLengthKnown || precision != 0 || scale != 0 || decimalKnown || nullable || nullableKnown {
			t.Fatal("detached computed metadata", meta)
		}
		levenshteinControlRows(t, old.rows, old.step)
	}
	run(check)
	run(fixture.Prepared(check))
	run(soundexLiteralView())
	run(fixture.Prepared(soundexLiteralView()))
	soundexInventory(t, p, b, c)
	extensionControlsFailures(t, p, c, b.Recorder, dump, check)
	bad := "CREATE SCHEMA sx_addon; CREATE EXTENSION fuzzystrmatch SCHEMA sx_addon VERSION 'missing';"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
		t.Fatal("invalid version accepted")
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &daitchRestoreCancelReader{cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("staged cancellation", err)
	}
	run(check)
	run(sentinel)
	soundexInventory(t, p, b, c)
	fixture.RunStep(t, p.Ctx, held, b.Recorder, frame)
	if extensionDump(t, p.Ctx, p.Native) != dump {
		t.Fatal("failed replacement changed provider or consumers")
	}
	other := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "sx_destination")
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	for _, s := range []fixture.Step{check, soundexLiteralView(), sentinel, frame} {
		rows, err := other.QueryContext(p.Ctx, s.Query, s.Args...)
		if err != nil {
			t.Fatal(err)
		}
		metaphoneControlMetadata(t, rows, s)
		levenshteinControlRows(t, rows, s)
	}
	if extensionDump(t, p.Ctx, other) != dump {
		t.Fatal("independent symbolic replay changed")
	}
}
