package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryFromValuesInsertDefaults(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) {
					s.Prepared = prepared
					fixture.DefaultReferenceRunStep(t, p, backend.DB, backend.Recorder, s)
				}
				run(fixture.Exec(`CREATE TABLE d(id INT,s TEXT DEFAULT CAST(1 AS TEXT),i INT DEFAULT int4('42'::text),b BOOL DEFAULT (DATE '0001-01-01 BC' < DATE '0001-01-01'),d DATE DEFAULT DATE '2000-02-29',v BYTEA DEFAULT '\x0080ff5c27')`, 0))
				run(fixture.Exec("INSERT INTO d(id) VALUES(5) ORDER BY 1", 1))
				run(fixture.Exec("INSERT INTO d(id) SELECT * FROM (VALUES(6),(6)) v(id)", 2))
				var date any = "2000-02-29"
				if backend.PG {
					date = time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC)
				}
				row := func(id int64) []any { return []any{id, "1", int64(42), true, date, "\x00\x80\xff\\'"} }
				run(fixture.Select("SELECT * FROM d ORDER BY id", []string{"id", "s", "i", "b", "d", "v"}, []string{"INT4", "TEXT", "INT4", "BOOL", "DATE", "BYTEA"}, [][]any{row(5), row(6), row(6)}))
				run(fixture.Exec("CREATE TABLE narrow(n SMALLINT)", 0))
				run(fixture.Exec("INSERT INTO narrow VALUES(9)", 1))
				failure := fixture.ImplicitError("INSERT INTO narrow SELECT * FROM (VALUES(1),(32768)) v(n)", fixture.Diagnostic{Code: "22003", Message: "smallint out of range"})
				failure.ErrorStage = "execute"
				run(failure)
				run(fixture.Select("SELECT * FROM narrow", []string{"n"}, []string{"INT2"}, [][]any{{int64(9)}}))
				run(fixture.Exec("CREATE TABLE nn(id INT,v INT DEFAULT 7,n INT NOT NULL)", 0))
				run(fixture.Exec("INSERT INTO nn VALUES(0,9,9)", 1))
				failure = fixture.ImplicitError("INSERT INTO nn(id,n) SELECT * FROM (VALUES(1,9),(2,NULL)) v(id,n)", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (2, 7, null).", Schema: "public", Table: "nn", Column: "n"})
				failure.ErrorStage = "execute"
				run(failure)
				remaining := fixture.Select("SELECT * FROM nn", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(9), int64(9)}})
				remaining.Prepared = prepared
				fixture.TableDDLMetadata(t, p.Ctx, backend.DB, backend.PG, prepared, remaining.Query, remaining.Columns, remaining.Types, []bool{true, true, false}, len(remaining.Rows))
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, remaining)
				for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE dst(n INT)", 0), fixture.Exec("INSERT INTO dst VALUES(7),(8),(9) OFFSET 1 FETCH FIRST ROW ONLY", 1), fixture.Select("SELECT * FROM dst", []string{"n"}, []string{"INT4"}, [][]any{{int64(8)}})} {
					run(s)
				}
			}
		})
	}
}

func TestReferenceQueryFromValuesInsertTransactions(t *testing.T) {
	for _, mode := range []string{"commit", "rollback", "failure"} {
		t.Run(mode, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
				run(fixture.Exec("CREATE TABLE t(n INT NOT NULL)", 0))
				run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
				tx, err := backend.DB.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				own := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s) }
				own(fixture.Prepared(fixture.Exec("INSERT INTO t SELECT t.n FROM t CROSS JOIN (VALUES(7),(7)) v(n)", 4)))
				want := [][]any{{int64(1)}, {int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {int64(2)}}
				own(fixture.Prepared(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, want)))
				committed := [][]any{{int64(1)}, {int64(2)}}
				run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, committed))
				if mode == "commit" {
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				} else {
					if mode == "failure" {
						failure := fixture.ImplicitError("INSERT INTO t SELECT * FROM (VALUES(NULL::integer)) v(n)", fixture.Diagnostic{Code: "23502", Message: `null value in column "n" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "n"})
						failure.ErrorStage = "execute"
						own(failure)
						failure = fixture.ImplicitError("SELECT n FROM t", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
						failure.ErrorStage = "execute"
						own(failure)
					}
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					want = committed
				}
				run(fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, want))
			}
		})
	}
}

func TestReferenceQueryFromValuesDecoratedCommandMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE order_keys(i INT)", 0))
		run(fixture.Exec("INSERT INTO order_keys VALUES(9)", 1))
		run(fixture.Exec("INSERT INTO order_keys VALUES(1),(2) ORDER BY +2", 2))
		q := "INSERT INTO order_keys VALUES(3),(4) ORDER BY +1.5 DESC"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{backend.DB, stmt, q}
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 2))
		run(fixture.Select("INSERT INTO order_keys VALUES(5),(6) ORDER BY +2 ASC", []string{}, []string{}, [][]any{}))
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{}, []string{}, [][]any{}))
		run(fixture.Select("SELECT i FROM order_keys ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(4)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(9)}}))
	}
}
