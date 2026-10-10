package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceMergeInsertOnCarrierBoundary(t *testing.T) {
	mergeInsertScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, prepared bool) {
		run := mergeInsertRun(t, p, b, prepared)
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(99)", 1))
		run(referenceExec("CREATE TABLE source(id INT,v DOUBLE PRECISION)", 0))
		run(referenceExec("INSERT INTO source VALUES(1,1.5)", 1))
		q := "MERGE INTO t USING source s ON s.v WHEN NOT MATCHED THEN INSERT VALUES(s.id)"
		if b.pg {
			run(referenceExec(q, 0))
		} else {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "MERGE ON type is unsupported", Position: referencePosition(strings.Index(q, "s.v") + 1)})
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			run(s)
		}
		run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
	})
}
