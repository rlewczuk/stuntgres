package reference_test

import "testing"

func TestReferenceViewReplaceCapabilityBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("INSERT INTO t VALUES(7)", 1))
		run(referenceExec("CREATE VIEW v AS SELECT id FROM t", 0))
		for _, query := range []string{"CREATE VIEW excluded WITH(check_option=local) AS SELECT id FROM t", "CREATE OR REPLACE VIEW v WITH(check_option=local) AS SELECT id FROM t", "ALTER VIEW v SET(check_option=local)"} {
			if backend.pg {
				run(referenceExec(query, 0))
			} else {
				run(referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: "view check options are unsupported"}))
			}
		}
		for _, query := range []string{"ALTER VIEW v ALTER COLUMN id SET DEFAULT 7", "ALTER VIEW v ALTER COLUMN id DROP DEFAULT"} {
			if backend.pg {
				run(referenceExec(query, 0))
			} else {
				run(referenceImplicitError(query, referenceDiagnostic{Code: "0A000", Message: "ALTER VIEW column defaults are unsupported", Position: "14"}))
			}
		}
		run(referenceExec("ALTER VIEW v RESET(unknown_option,unknown_option,check_option)", 0))
		run(referenceSelect("SELECT id FROM v", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
}
