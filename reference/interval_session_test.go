package reference_test

import (
	"context"
	"errors"
	"stuntgres"
	"testing"
)

func TestReferenceIntervalSessionReuseLocalAndReplacement(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		backend.db.SetMaxOpenConns(2)
		backend.db.SetMaxIdleConns(1)
		a, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.recorder, s) }
		run(referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
		run(referenceExec("INSERT INTO iv VALUES('1 day')", 1))
		run(referenceExec("SET IntervalStyle=iso_8601", 0))
		referenceRunStep(t, p.ctx, b, backend.recorder, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, q := range []string{"SET LOCAL IntervalStyle=postgres_verbose", "SAVEPOINT mark", "SET LOCAL IntervalStyle=sql_standard", "ROLLBACK TO mark"} {
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceExec(q, 0))
		}
		referenceRunStep(t, p.ctx, tx, backend.recorder, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"@ 1 day"}}))
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		run(referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"P1D"}}))
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
		// b stays borrowed, so the only idle connection is the configured a.
		a, err = backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		run(referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"iso_8601"}}))
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
		if err = b.Close(); err != nil {
			t.Fatal(err)
		}
		backend.db.SetMaxIdleConns(0)
		backend.db.SetMaxIdleConns(1)
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SHOW IntervalStyle", []string{"IntervalStyle"}, []string{"TEXT"}, [][]any{{"postgres"}}))
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
	}
}

func TestReferenceIntervalCancellationBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE iv(v INTERVAL)", 0))
	p.step(t, referenceExec("INSERT INTO iv VALUES('1 day')", 1))
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	for _, backend := range p.backends() {
		stmt, err := backend.db.PrepareContext(canceled, "INSERT INTO iv VALUES('1 mon')")
		if !errors.Is(err, context.Canceled) || stmt != nil {
			t.Fatal(stmt, err)
		}
		rows, err := backend.db.QueryContext(canceled, "SELECT v FROM iv")
		if !errors.Is(err, context.Canceled) || rows != nil {
			t.Fatal(rows, err)
		}
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM iv", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}}))
		stmt, err = backend.db.PrepareContext(p.ctx, "INSERT INTO iv VALUES($1)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		buffer := []byte("1 mon")
		referenceRunStep(t, p.ctx, referenceHeldStatement{backend.db, stmt, "INSERT INTO iv VALUES($1)"}, backend.recorder, referenceExec("INSERT INTO iv VALUES($1)", 1, buffer))
		buffer[0] = '9'
		referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT v FROM iv ORDER BY v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}, {"1 mon"}}))
	}
	if err := stuntgres.Reset(canceled, p.native); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	queryCtx, stop := context.WithCancel(p.ctx)
	rows, err := p.memory.QueryContext(queryCtx, "SELECT v FROM iv")
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
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SELECT v FROM iv ORDER BY v", []string{"v"}, []string{"INTERVAL"}, [][]any{{"1 day"}, {"1 mon"}}))
}
