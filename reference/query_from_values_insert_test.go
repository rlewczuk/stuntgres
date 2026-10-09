package reference_test

import (
	"fmt"
	"testing"
	"time"
)

func TestReferenceQueryFromValuesInsertDefaults(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				run := func(s referenceStep) { s.prepared = prepared; defaultReferenceRunStep(t, p, backend.db, backend.recorder, s) }
				run(referenceExec(`CREATE TABLE d(id INT,s TEXT DEFAULT CAST(1 AS TEXT),i INT DEFAULT int4('42'::text),b BOOL DEFAULT (DATE '0001-01-01 BC' < DATE '0001-01-01'),d DATE DEFAULT DATE '2000-02-29',v BYTEA DEFAULT '\x0080ff5c27')`, 0))
				run(referenceExec("INSERT INTO d(id) VALUES(5) ORDER BY 1", 1))
				run(referenceExec("INSERT INTO d(id) SELECT * FROM (VALUES(6),(6)) v(id)", 2))
				var date any = "2000-02-29"
				if backend.pg { date = time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC) }
				row := func(id int64) []any { return []any{id, "1", int64(42), true, date, "\x00\x80\xff\\'"} }
				run(referenceSelect("SELECT * FROM d ORDER BY id", []string{"id", "s", "i", "b", "d", "v"}, []string{"INT4", "TEXT", "INT4", "BOOL", "DATE", "BYTEA"}, [][]any{row(5), row(6), row(6)}))
				run(referenceExec("CREATE TABLE narrow(n SMALLINT)", 0))
				run(referenceExec("INSERT INTO narrow VALUES(9)", 1))
				failure := referenceImplicitError("INSERT INTO narrow SELECT * FROM (VALUES(1),(32768)) v(n)", referenceDiagnostic{Code: "22003", Message: "smallint out of range"})
				failure.errorStage = "execute"
				run(failure)
				run(referenceSelect("SELECT * FROM narrow", []string{"n"}, []string{"INT2"}, [][]any{{int64(9)}}))
				run(referenceExec("CREATE TABLE nn(id INT,v INT DEFAULT 7,n INT NOT NULL)", 0))
				run(referenceExec("INSERT INTO nn VALUES(0,9,9)", 1))
				failure = referenceImplicitError("INSERT INTO nn(id,n) SELECT * FROM (VALUES(1,9),(2,NULL)) v(id,n)", referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (2, 7, null).", Schema: "public", Table: "nn", Column: "n"})
				failure.errorStage = "execute"
				run(failure)
				remaining := referenceSelect("SELECT * FROM nn", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(9), int64(9)}})
				remaining.prepared = prepared
				referenceTableDDLMetadata(t, p.ctx, backend.db, backend.pg, prepared, remaining.query, remaining.columns, remaining.types, []bool{true, true, false}, len(remaining.rows))
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, remaining)
				for _, s := range []referenceStep{referenceExec("CREATE TABLE dst(n INT)", 0), referenceExec("INSERT INTO dst VALUES(7),(8),(9) OFFSET 1 FETCH FIRST ROW ONLY", 1), referenceSelect("SELECT * FROM dst", []string{"n"}, []string{"INT4"}, [][]any{{int64(8)}})} { run(s) }
			}
		})
	}
}

func TestReferenceQueryFromValuesInsertTransactions(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "failure"} {
		t.Run(mode, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
				run(referenceExec("CREATE TABLE t(n INT NOT NULL)", 0))
				run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
				tx, err := backend.db.BeginTx(p.ctx, nil)
				if err != nil { t.Fatal(err) }
				defer tx.Rollback()
				own := func(s referenceStep) { referenceRunStep(t, p.ctx, tx, backend.recorder, s) }
				own(referencePrepared(referenceExec("INSERT INTO t SELECT t.n FROM t CROSS JOIN (VALUES(7),(7)) v(n)", 4)))
				want := [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}}
				own(referencePrepared(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, want)))
				committed := [][]any{{int64(1)}, {int64(2)}}
				run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, committed))
				if mode == "commit" {
					if err := tx.Commit(); err != nil { t.Fatal(err) }
				} else {
					if mode == "failure" {
						failure := referenceImplicitError("INSERT INTO t SELECT * FROM (VALUES(NULL::integer)) v(n)", referenceDiagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "n"})
						failure.errorStage = "execute"
						own(failure)
						failure = referenceImplicitError("SELECT n FROM t", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
						failure.errorStage = "execute"
						own(failure)
					}
					if err := tx.Rollback(); err != nil { t.Fatal(err) }
					want = committed
				}
				run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, want))
			}
		})
	}
}

func TestReferenceQueryFromValuesDecoratedCommandMetadata(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE order_keys(i INT)", 0))
		run(referenceExec("INSERT INTO order_keys VALUES(9)", 1))
		run(referenceExec("INSERT INTO order_keys VALUES(1),(2) ORDER BY +2", 2))
		q := "INSERT INTO order_keys VALUES(3),(4) ORDER BY +1.5 DESC"
		stmt, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := referenceHeldStatement{backend.db, stmt, q}
		referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 2))
		run(referenceSelect("INSERT INTO order_keys VALUES(5),(6) ORDER BY +2 ASC", []string{}, []string{}, [][]any{}))
		referenceRunStep(t, p.ctx, held, backend.recorder, referenceSelect(q, []string{}, []string{}, [][]any{}))
		run(referenceSelect("SELECT i FROM order_keys ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(4)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(9)}}))
	}
}
