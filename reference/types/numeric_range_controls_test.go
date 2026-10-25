package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func numericRangeControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, checks []fixture.Step, failures []struct{ script string; diagnostic fixture.Diagnostic }) {
	t.Helper()
	if b.PG { return } // Public native controls have no PostgreSQL API counterpart.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
	held, err := b.DB.QueryContext(p.Ctx, checks[0].Query)
	if err != nil { t.Fatal(err) }
	defer held.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	fixture.RangeConsumeHeld(t, held, checks[0])
	for _, s := range checks { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	for _, s := range checks { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE allocator_spacer(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	assertUnchanged := func() {
		for _, s := range checks {
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s)
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(s))
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) { t.Fatal("redump changed", err, redump.String()) }
	}
	assertUnchanged()
	for _, failure := range failures {
		held, err := target.Memory.QueryContext(target.Ctx, checks[0].Query)
		if err != nil { t.Fatal(err) }
		defer held.Close()
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(failure.script)), &failure.diagnostic)
		fixture.RangeConsumeHeld(t, held, checks[0])
		assertUnchanged()
	}
	canceled, cancel := context.WithCancel(target.Ctx)
	cancel()
	for _, call := range []func() error{
		func() error { return stuntgres.Dump(canceled, target.Native, &bytes.Buffer{}) },
		func() error { return stuntgres.Reset(canceled, target.Native) },
		func() error { return stuntgres.Restore(canceled, target.Native, bytes.NewReader(dump.Bytes())) },
	} {
		err := call()
		var state interface{ SQLState() string }
		if !errors.Is(err, context.Canceled) || errors.As(err, &state) { t.Fatal("control cancellation must be ordinary", err) }
	}
	assertUnchanged()
	held, err = target.Memory.QueryContext(target.Ctx, checks[0].Query)
	if err != nil { t.Fatal(err) }
	defer held.Close()
	if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE TABLE r(v text NOT NULL); INSERT INTO r VALUES ('replacement');")); err != nil { t.Fatal(err) }
	fixture.RangeConsumeHeld(t, held, checks[0])
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM r", []string{"v"}, []string{"TEXT"}, [][]any{{"replacement"}}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil { t.Fatal("actual native dump replay", err) }
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		for _, s := range checks {
			step := rangeSelect(pg, s.Query, s.Columns, s.Types, s.Rows, make([]bool, len(s.Columns)))
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, step)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil { t.Fatal(err) }
	}
}

func TestReferenceNumericRangeControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k integer NOT NULL,v numrange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE vacant(v numrange NOT NULL)", 0))
		want := [][]any{}
		for i, tc := range numericRangeValues() {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES ($1,$2)", 1, i+1, tc.input))
			want = append(want, []any{int64(i+1), tc.output})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES (100,NULL)", 1))
		want = append(want, []any{int64(100), nil})
		checks := []fixture.Step{
			rangeSelect(b, "SELECT k,v FROM r ORDER BY k", []string{"k", "v"}, []string{"INT4", "NUMRANGE"}, want, []bool{false, true}),
			rangeSelect(b, "SELECT v FROM vacant", []string{"v"}, []string{"NUMRANGE"}, nil, []bool{false}),
		}
		for _, s := range checks { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		failures := []struct{ script string; diagnostic fixture.Diagnostic }{}
		for _, tc := range numericRangeFailures() {
			script := "CREATE TABLE staged(v numrange); INSERT INTO staged VALUES ('[7,8)'); INSERT INTO staged VALUES ("+rangeLiteral(tc.input)+");"
			want := numericRangeDiagnostic(tc)
			want.Position = strconv.Itoa(utf8.RuneCountInString(script[:strings.LastIndex(script, "('")+1])+1)
			failures = append(failures, struct{ script string; diagnostic fixture.Diagnostic }{script, want})
		}
		numericRangeControlCycle(t, p, b, checks, failures)
	}
}
