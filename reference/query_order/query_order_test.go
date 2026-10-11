package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderRegression(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
	for _, step := range []fixture.Step{
		fixture.Select("SELECT id,a+b AS k FROM t ORDER BY k DESC NULLS LAST,id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(6)}, {int64(1), int64(3)}, {int64(4), int64(2)}, {int64(3), nil}}),
		fixture.Select("SELECT id FROM t ORDER BY a ASC NULLS FIRST,b DESC,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}, {int64(4)}}),
	} {
		t.Run(step.Query, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) { p.Step(t, step) })
			t.Run("prepared", func(t *testing.T) { p.Step(t, fixture.Prepared(step)) })
		})
	}
	t.Run("ambiguous-alias", func(t *testing.T) {
		step := fixture.ImplicitError("SELECT a AS x,b AS x FROM t ORDER BY x", fixture.Diagnostic{
			Code: "42702", Message: `ORDER BY "x" is ambiguous`, Position: "38",
		})
		step.SelectRows = true
		step.ErrorStage = "execute"
		t.Run("direct", func(t *testing.T) { p.Step(t, step) })
		step.ErrorStage = "prepare"
		t.Run("prepared", func(t *testing.T) { p.Step(t, fixture.Prepared(step)) })
	})
	p.Step(t, fixture.Exec("CREATE TABLE v(id INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO v VALUES(1),(2),(3)", 3))
	for _, key := range []string{"n", "1", "nextval('s')"} {
		for _, prepared := range []bool{false, true} {
			t.Run(key+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p.Step(t, fixture.Exec("CREATE SEQUENCE s START WITH 1", 0))
				step := fixture.Select("SELECT nextval('s') AS n FROM v ORDER BY "+key+" DESC", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}})
				step.Prepared = prepared
				// Separate backend subtests still exercise PostgreSQL if native fails.
				p.Step(t, step)
				p.Step(t, fixture.Select("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
				p.Step(t, fixture.Exec("DROP SEQUENCE s", 0))
			})
		}
	}
}
