package reference_test

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceAlterNullabilityReviewCascadeNotice(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					var notices []referenceDiagnostic
					_, conn, recorder := noticeReferencePool(t, p, backend.pg, &notices)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT,c INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,9,11)", 1))
					run(referenceExec("CREATE SEQUENCE owned OWNED BY t.b", 0))
					run(referenceExec("CREATE VIEW v AS SELECT b,c FROM t", 0))
					run(referenceExec("CREATE VIEW w AS SELECT * FROM v", 0))
					if len(notices) != 0 {
						t.Fatal("unexpected setup notice", notices)
					}
					step := referenceExec("ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", 0)
					step.prepared = prepared
					run(step)
					want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to view v\ndrop cascades to view w"}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatal("selected cascade notice", notices, want)
					}
					run(referenceSelect("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}
