package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringLiteralSourceDollarConsumers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) {
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE dollar_params(id INT,label TEXT)", 0))
			for i, prepared := range []bool{false, true} {
				s := fixture.Exec(`INSERT INTO dollar_params VALUES ($1,$tag$$2;$tag$)`, 1, 7+i)
				s.Prepared = prepared
				// The original parameter writes were Context methods.
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			s := fixture.Select("SELECT * FROM dollar_params ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "$2;"}, {int64(8), "$2;"}})
			run(s)
			q := `SELECT id FROM dollar_params WHERE label=$$ $1 $$ ORDER BY id`
			stmt, err := backend.DB.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.Query()
			if err != nil {
				stmt.Close()
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, fixture.Select(q, []string{"id"}, []string{"INT4"}, nil), nil)
			rows, err = stmt.Query(1)
			if rows != nil {
				rows.Close()
				t.Fatal("extra argument exposed rows")
			}
			var state interface{ SQLState() string }
			if err == nil || errors.As(err, &state) {
				t.Fatalf("extra argument must be ordinary: %v", err)
			}
			stmt.Close()
			run(fixture.Exec("CREATE TABLE separated(x$tag$x$tag$ TEXT)", 0))
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec(`INSERT INTO separated(x$tag$x$tag$) VALUES ($$v$$)`, 1)
				s.Prepared = prepared
				run(s)
			}
			run(fixture.Select("SELECT x$tag$x$tag$ FROM separated ORDER BY x$tag$x$tag$", []string{"x$tag$x$tag$"}, []string{"TEXT"}, [][]any{{"v"}, {"v"}}))
			run(fixture.Exec("CREATE TABLE t(label TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES ('seed')", 1))
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err = backend.DB.ExecContext(canceled, `INSERT INTO t VALUES ($$canceled$$)`)
			fixture.LexicalOrdinary(t, err, context.Canceled)
			stmt, err = backend.DB.Prepare(`INSERT INTO t VALUES ($$canceled$$)`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := stmt.ExecContext(canceled)
			if result != nil {
				t.Fatal("canceled held execution exposed a result")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			run(fixture.Select("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
		})
	}
}
