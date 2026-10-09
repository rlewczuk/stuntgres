package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceAlterAddColumnMixedDefaultBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(1)", 1))
		query := "ALTER TABLE t ADD b INT DEFAULT 7, ALTER b SET DEFAULT 9"
		if backend.pg {
			run(referenceExec(query, 0))
			run(referenceExec("INSERT INTO t(id) VALUES(2)", 1))
			run(referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(2), int64(9)}}))
		} else {
			position := referencePosition(len([]rune(query[:strings.LastIndex(query, "ALTER ")])) + 1)
			failure := referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: "ALTER TABLE action lists are unsupported", Position: position})
			failure.errorStage = "execute"
			run(failure)
			run(referenceSelect("SELECT * FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}
	}
}
