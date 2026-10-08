package reference_test

import "testing"

func TestReferenceQueryOrderRegression(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,a INT,b INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,2,1),(2,1,5),(3,NULL,2),(4,2,0)", 4))
	for _, step := range []referenceStep{
		referenceSelect("SELECT id,a+b AS k FROM t ORDER BY k DESC NULLS LAST,id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(6)}, {int64(1), int64(3)}, {int64(4), int64(2)}, {int64(3), nil}}),
		referenceSelect("SELECT id FROM t ORDER BY a ASC NULLS FIRST,b DESC,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}, {int64(4)}}),
	} {
		t.Run(step.query, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) { p.step(t, step) })
			t.Run("prepared", func(t *testing.T) { p.step(t, referencePrepared(step)) })
		})
	}
	t.Run("ambiguous-alias", func(t *testing.T) {
		step := referenceImplicitError("SELECT a AS x,b AS x FROM t ORDER BY x", referenceDiagnostic{
			Code: "42702", Message: `ORDER BY "x" is ambiguous`, Position: "38",
		})
		step.selectRows = true
		step.errorStage = "execute"
		t.Run("direct", func(t *testing.T) { p.step(t, step) })
		step.errorStage = "prepare"
		t.Run("prepared", func(t *testing.T) { p.step(t, referencePrepared(step)) })
	})
	p.step(t, referenceExec("CREATE TABLE v(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO v VALUES(1),(2),(3)", 3))
	for _, key := range []string{"n", "1", "nextval('s')"} {
		for _, prepared := range []bool{false, true} {
			t.Run(key+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
				step := referenceSelect("SELECT nextval('s') AS n FROM v ORDER BY "+key+" DESC", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}, {int64(2)}, {int64(1)}})
				step.prepared = prepared
				// Separate backend subtests still exercise PostgreSQL if native fails.
				p.step(t, step)
				p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(4)}}))
				p.step(t, referenceExec("DROP SEQUENCE s", 0))
			})
		}
	}
}
