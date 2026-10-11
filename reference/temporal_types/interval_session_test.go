package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceIntervalSessionReuseLocalAndReplacement(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		backend.DB.SetMaxOpenConns(2)
		backend.DB.SetMaxIdleConns(1)
		a, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE iv(v INTERVAL)", 0))
		run(fixture.Exec("INSERT INTO iv VALUES('1 day')", 1))
		run(fixture.Exec("SET IntervalStyle=iso_8601", 0))
		fixture.RunStep(t, p.Ctx, b, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
		tx, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, q := range []string{"SET LOCAL IntervalStyle=postgres_verbose", "SAVEPOINT mark", "SET LOCAL IntervalStyle=sql_standard", "ROLLBACK TO mark"} {
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"@ 1 day"}}))
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"P1D"}}))
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
		// b stays borrowed, so the only idle connection is the configured a.
		a, err = backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
		if err = b.Close(); err != nil {
			t.Fatal(err)
		}
		backend.DB.SetMaxIdleConns(0)
		backend.DB.SetMaxIdleConns(1)
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"postgres"}}))
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
	}
}

func TestReferenceIntervalCancellationBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE iv(v INTERVAL)", 0))
	p.Step(t, fixture.Exec("INSERT INTO iv VALUES('1 day')", 1))
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, backend := range p.Backends() {
		stmt, err := backend.DB.PrepareContext(canceled, "INSERT INTO iv VALUES('1 mon')")
		if !errors.Is(err, context.Canceled) || stmt != nil {
			t.Fatal(stmt, err)
		}
		rows, err := backend.DB.QueryContext(canceled, "SELECT v FROM iv")
		if !errors.Is(err, context.Canceled) || rows != nil {
			t.Fatal(rows, err)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
		stmt, err = backend.DB.PrepareContext(p.Ctx, "INSERT INTO iv VALUES($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		buffer := []byte("1 mon")
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, "INSERT INTO iv VALUES($1)"}, backend.Recorder, fixture.Exec("INSERT INTO iv VALUES($1)", 1, buffer))
		buffer[0] = '9'
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT v FROM iv ORDER BY v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}, {"1 mon"}}))
	}
	if err := stuntgres.Reset(canceled, p.Native); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	queryCtx, stop := context.WithCancel(p.Ctx)
	rows, err := p.Memory.QueryContext(queryCtx, "SELECT v FROM iv")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	stop()
	for rows.Next() {
	}
	if !errors.Is(rows.Err(), context.Canceled) {
		t.Fatal(rows.Err())
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, fixture.Select("SELECT v FROM iv ORDER BY v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}, {"1 mon"}}))
}
