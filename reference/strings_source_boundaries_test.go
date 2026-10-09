package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceStringLiteralSourceDollarConsumers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(referenceExec("CREATE TABLE dollar_params(id INT,label TEXT)", 0))
			for i, prepared := range []bool{false, true} {
				s := referenceExec(`INSERT INTO dollar_params VALUES ($1,$tag$$2;$tag$)`, 1, 7+i)
				s.prepared = prepared
				// The original parameter writes were Context methods.
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			s := referenceSelect("SELECT * FROM dollar_params ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "$2;"}, {int64(8), "$2;"}})
			run(s)
			q := `SELECT id FROM dollar_params WHERE label=$$ $1 $$ ORDER BY id`
			stmt, err := backend.db.Prepare(q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := stmt.Query()
			if err != nil {
				stmt.Close()
				t.Fatal(err)
			}
			lexicalRows(t, backend.pg, rows, referenceSelect(q, []string{"id"}, []string{"INT4"}, nil), nil)
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
			run(referenceExec("CREATE TABLE separated(x$tag$x$tag$ TEXT)", 0))
			for _, prepared := range []bool{false, true} {
				s := referenceExec(`INSERT INTO separated(x$tag$x$tag$) VALUES ($$v$$)`, 1)
				s.prepared = prepared
				run(s)
			}
			run(referenceSelect("SELECT x$tag$x$tag$ FROM separated ORDER BY x$tag$x$tag$", []string{"x$tag$x$tag$"}, []string{"TEXT"}, [][]any{{"v"}, {"v"}}))
			run(referenceExec("CREATE TABLE t(label TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES ('seed')", 1))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			_, err = backend.db.ExecContext(canceled, `INSERT INTO t VALUES ($$canceled$$)`)
			lexicalOrdinary(t, err, context.Canceled)
			stmt, err = backend.db.Prepare(`INSERT INTO t VALUES ($$canceled$$)`)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := stmt.ExecContext(canceled)
			if result != nil {
				t.Fatal("canceled held execution exposed a result")
			}
			lexicalOrdinary(t, err, context.Canceled)
			run(referenceSelect("SELECT * FROM t", []string{"label"}, []string{"TEXT"}, [][]any{{"seed"}}))
		})
	}
}
