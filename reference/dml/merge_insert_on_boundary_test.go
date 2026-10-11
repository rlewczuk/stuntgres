package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMergeInsertOnCarrierBoundary(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(99)", 1))
		run(fixture.Exec("CREATE TABLE source(id INT,v DOUBLE PRECISION)", 0))
		run(fixture.Exec("INSERT INTO source VALUES(1,1.5)", 1))
		q := "MERGE INTO t USING source s ON s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
		if b.PG {
			run(fixture.Exec(q, 0))
		} else {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "MERGE ON type is unsupported", Position: fixture.Position(strings.Index(q, "s.v") + 1)})
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		run(fixture.Select("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
	})
}
