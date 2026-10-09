package reference_test

import "testing"

func TestReferenceViewWriteCapabilityBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE t(id INT)", 0), referenceExec("INSERT INTO t VALUES(7)", 1), referenceExec("CREATE VIEW v AS SELECT id FROM t", 0)} {
				run(s)
			}
			queries := []string{"INSERT INTO v VALUES(8)", "UPDATE v SET id=8", "DELETE FROM v"}
			if b.pg {
				expected := [][][]any{{{int64(7)}, {int64(8)}}, {{int64(8)}, {int64(8)}}, {}}
				for i, q := range queries {
					count := int64(2)
					if i == 0 {
						count = 1
					}
					run(referenceExec(q, count))
					s := referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, expected[i])
					s.bag = true
					run(s)
				}
			} else {
				for _, q := range queries {
					s := referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "view writes are unsupported"})
					s.errorStage = "execute"
					run(s)
				}
				run(referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		})
	}
}

func TestReferenceViewInternalCHARStaysProviderBounded(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			if b.pg {
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec(`CREATE TABLE bad(x "char")`, 0))
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT x FROM bad", []string{"x"}, []string{"CHAR"}, nil))
			} else {
				s := referenceImplicitError(`CREATE TABLE bad(x "char")`, referenceDiagnostic{Code: "0A000", Message: "unsupported column type", Position: "20"})
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
		})
	}
}
