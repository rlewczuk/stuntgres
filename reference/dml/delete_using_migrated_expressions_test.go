package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingMigratedExpressions(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := deleteUsingNonContextRun(t, p, b, b.DB)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT,f BOOL)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE)", 3))
		q := "DELETE FROM t AS d WHERE d.v IS NOT DISTINCT FROM $1"
		stmt, err := b.DB.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		first := fixture.Exec(q, 1, nil)
		first.NonContext = true
		fixture.RunStep(t, p.Ctx, deleteUsingNonContextHeld{HeldStatement: fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}, direct: b.DB}, b.Recorder, first)
		decisive := "DELETE FROM t WHERE FALSE AND int4($1::text)=id"
		if b.PG {
			run(fixture.Exec(decisive, 0, "bad"))
		} else {
			run(fixture.ErrorDiagnostic(decisive, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), "bad"))
		}
		for _, query := range []string{
			"DELETE FROM t d WHERE d.v::int4=10",
			"DELETE FROM t d WHERE CAST(d.v AS int4)=10",
			"DELETE FROM t d WHERE int4(d.v)=10",
		} {
			for _, prepared := range []bool{false, true} {
				tx, err := b.DB.Begin()
				if err != nil {
					t.Fatal(err)
				}
				boundaryRun := func(step fixture.Step) { step.NonContext = true; fixture.RunStep(t, p.Ctx, tx, b.Recorder, step) }
				step := fixture.Exec(query, 1)
				step.Prepared = prepared
				boundaryRun(step)
				boundaryRun(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
			}
		}
		run(fixture.Exec("DELETE FROM t AS d WHERE d.f IS FALSE AND CAST(v AS BIGINT)=20", 1))
		run(fixture.Exec("DELETE FROM t AS d WHERE d.v ISNULL", 0))
		run(fixture.Exec("DELETE FROM t AS d WHERE d.f IS TRUE AND v::int8=99", 0))
		run(fixture.Exec("DELETE FROM t WHERE int8(v)=99", 0))
		run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(fixture.Exec("CREATE TABLE frames(v INT)", 0))
		run(fixture.Exec("INSERT INTO frames VALUES(10),(NULL),(20)", 3))
		q = "DELETE FROM frames WHERE v IS NOT DISTINCT FROM int4($1::text)"
		held, err := b.DB.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		executor := deleteUsingNonContextHeld{HeldStatement: fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: q}, direct: b.DB}
		for _, call := range []struct {
			arg       any
			bad       bool
			survivors [][]any
		}{
			{"bad", true, [][]any{{int64(10)}, {nil}, {int64(20)}}},
			{"10", false, [][]any{{nil}, {int64(20)}}},
			{nil, false, [][]any{{int64(20)}}},
			{"bad", true, [][]any{{int64(20)}}},
			{"20", false, nil},
		} {
			step := fixture.Exec(q, 1, call.arg)
			if call.bad {
				step = fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`), call.arg)
			}
			step.NonContext = true
			fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
			rows := fixture.Select("SELECT v FROM frames", []string{"v"}, []string{"INT4"}, call.survivors)
			rows.Bag = true
			run(rows)
		}
	})
}
