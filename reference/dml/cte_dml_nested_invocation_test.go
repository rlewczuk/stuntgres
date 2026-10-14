package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCTEDMLNestedInvocation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { cteDMLRun(t, p, b, false, s) }
					cteDMLSeed(t, p, b)
					run(fixture.Exec("CREATE TABLE dst(id INT,v INT)", 0))
					run(fixture.Exec("INSERT INTO dst VALUES(1,0),(2,0)", 2))
					// Both the actual assignment and RETURNING scalar read the earlier
					// completed modifier. The materialized SELECT-only local owner must
					// be fresh per outer row and must still find its enclosing u spool.
					q := "WITH u AS (UPDATE t SET v=v+$1 RETURNING id,v), saved AS (UPDATE dst SET v=(SELECT u.v FROM u WHERE u.id=dst.id) RETURNING dst.id,dst.v,(SELECT u.v FROM u WHERE u.id=dst.id) AS replay) SELECT s.id,s.v,s.replay,(WITH local_value AS MATERIALIZED (SELECT s.id AS id,s.v+$2::int4 AS shifted) SELECT l.shifted+again.v FROM local_value l JOIN u again ON again.id=l.id) AS nested_value FROM saved s ORDER BY s.id"
					var held *sql.Stmt
					var executor fixture.Executor = b.DB
					if prepared {
						var err error
						held, err = b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						executor = fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: q}
					}
					// Preparing the SAME held object must not execute either modifier.
					run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}))
					run(fixture.Select("SELECT id,v FROM dst ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(0)}}))
					for _, call := range []struct {
						delta, offset int64
						rows, final   [][]any
					}{
						{1, 100, [][]any{{int64(1), int64(11), int64(11), int64(122)}, {int64(2), int64(21), int64(21), int64(142)}}, [][]any{{int64(1), int64(11)}, {int64(2), int64(21)}}},
						{2, 200, [][]any{{int64(1), int64(13), int64(13), int64(226)}, {int64(2), int64(23), int64(23), int64(246)}}, [][]any{{int64(1), int64(13)}, {int64(2), int64(23)}}},
					} {
						step := fixture.Select(q, []string{"id", "v", "replay", "nested_value"}, []string{"INT4", "INT4", "INT4", "INT4"}, call.rows, call.delta, call.offset)
						fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
						// Exact effects after EACH call prove replay did not rerun u.
						run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, call.final))
						run(fixture.Select("SELECT id,v FROM dst ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, call.final))
					}
					// Legal nested SELECT-only WITH above does not legalize a nested modifier.
					bad := "SELECT (WITH forbidden AS (UPDATE t SET v=v+100 RETURNING v) SELECT v FROM forbidden LIMIT 1)"
					failure := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "0A000", Message: "WITH clause containing a data-modifying statement must be at the top level", Position: cteDMLPosition(bad, "forbidden AS", false)})
					failure.SelectRows, failure.ErrorStage = true, "execute"
					if prepared {
						failure.ErrorStage = "prepare"
					}
					cteDMLRun(t, p, b, prepared, failure)
					for _, table := range []string{"t", "dst"} {
						run(fixture.Select("SELECT id,v FROM "+table+" ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(13)}, {int64(2), int64(23)}}))
					}
				})
			}
		})
	}
}
