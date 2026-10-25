package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func moreHeldLifecycle(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE more_live(k integer,a int4range,m int4multirange)", 0))
	run(fixture.Exec("INSERT INTO more_live VALUES (1,'[1,4]','{[1,3),[8,9)}')", 1))
	constructor := rangeSelect(b, "SELECT multirange(a) AS v FROM more_live ORDER BY k", []string{"v"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[1,5)}"}}, nil)
	expansion := rangeSelect(b, "SELECT unnest(m) AS v FROM more_live ORDER BY k", []string{"v"}, []string{"INT4RANGE"}, [][]any{{"[1,3)"}, {"[8,9)"}}, nil)
	moreBoth(t, p, b, constructor)
	moreBoth(t, p, b, expansion)
	if b.PG {
		// PostgreSQL has no Restore API. Use ordinary definition replacement
		// on one physical owner and reuse the exact server-prepared statement.
		// The successful held query is fully consumed before DROP takes its
		// DDL lock; native retained-row assertions remain below.
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer conn.Close()
		stale, err := conn.PrepareContext(p.Ctx, constructor.Query)
		if err != nil { t.Fatal(err) }
		defer stale.Close()
		held := fixture.HeldStatement{Executor: conn, Statement: stale, SQL: constructor.Query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, constructor)
		for _, s := range []fixture.Step{
			fixture.Exec("DROP TABLE more_live", 0),
			fixture.Exec("CREATE TABLE more_live(v text NOT NULL)", 0),
			fixture.Exec("INSERT INTO more_live VALUES ('replacement')", 1),
		} {
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		}
		failure := fixture.ErrorDiagnostic(constructor.Query, inspectDiagnostic(constructor.Query, "42703", `column "a" does not exist`, "", "a)", "", ""))
		failure.SelectRows, failure.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v FROM more_live", []string{"v"}, []string{"TEXT"}, [][]any{{"replacement"}}))
		return
	}
	var retained []*sql.Rows
	for _, check := range []fixture.Step{constructor, expansion} {
		rows, err := b.DB.QueryContext(p.Ctx, check.Query)
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		check.CheckMetadata(t, rows)
		retained = append(retained, rows)
	}
	other, err := b.DB.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	fixture.RunStep(t, p.Ctx, other, b.Recorder, fixture.Exec("INSERT INTO more_live VALUES (2,'[20,21)','{[20,21)}')", 1))
	if err := other.Close(); err != nil { t.Fatal(err) }
	fixture.RangeConsumeHeld(t, retained[0], constructor)
	fixture.RangeConsumeHeld(t, retained[1], expansion)
	constructor.Rows = append(constructor.Rows, []any{"{[20,21)}"})
	expansion.Rows = append(expansion.Rows, []any{"[20,21)"})
	moreBoth(t, p, b, constructor)
	moreBoth(t, p, b, expansion)
	retained = nil
	for _, check := range []fixture.Step{constructor, expansion} {
		rows, err := b.DB.QueryContext(p.Ctx, check.Query)
		if err != nil { t.Fatal(err) }
		defer rows.Close()
		retained = append(retained, rows)
	}
	stale, err := b.DB.PrepareContext(p.Ctx, constructor.Query)
	if err != nil { t.Fatal(err) }
	defer stale.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stale, SQL: constructor.Query}, b.Recorder, constructor)
	// A late CHECK failure must preserve both populated retained set/scalar
	// outputs and the complete committed catalog, not only an empty sentinel.
	var before, after bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil { t.Fatal(err) }
	bad := "CREATE TABLE more_late(a int4range,CONSTRAINT more_late_empty CHECK(isempty(multirange(a)))); INSERT INTO more_late VALUES ('empty'); INSERT INTO more_late VALUES ('[1,4]');"
	want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "more_late" violates check constraint "more_late_empty"`)
	want.Detail, want.Schema, want.Table, want.Constraint = "Failing row contains ([1,5)).", "public", "more_late", "more_late_empty"
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad)), &want)
	if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) { t.Fatal("failed Restore changed committed state", err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString("CREATE TABLE more_live(v text NOT NULL); INSERT INTO more_live VALUES ('replacement');")); err != nil { t.Fatal(err) }
	fixture.RangeConsumeHeld(t, retained[0], constructor)
	fixture.RangeConsumeHeld(t, retained[1], expansion)
	failure := fixture.ErrorDiagnostic(constructor.Query, inspectDiagnostic(constructor.Query, "42703", `column "a" does not exist`, "", "a)", "", ""))
	failure.SelectRows, failure.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b.DB, Statement: stale, SQL: constructor.Query}, b.Recorder, failure)
	run(fixture.Select("SELECT v FROM more_live", []string{"v"}, []string{"TEXT"}, [][]any{{"replacement"}}))
}
