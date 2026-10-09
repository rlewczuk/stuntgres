package reference_test

import (
	"fmt"
	"testing"
)

// The tagged DROP-CONSTRAINT family's original remains until its owner deduplicates it.
func TestReferenceNotNullDropSystemColumnDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			run := func(s referenceStep) { s.prepared = prepared; p.step(t, s) }
			run(referenceExec("CREATE TABLE t(a INT CONSTRAINT a_nn NOT NULL,oid INT)", 0))
			run(referenceExec("INSERT INTO t VALUES(7,9)", 1))
			for _, column := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
				s := referenceImplicitError("ALTER TABLE t ALTER "+column+" DROP NOT NULL", referenceDiagnostic{Code: "0A000", Message: `cannot alter system column "` + column + `"`})
				s.errorStage = "execute"
				run(s)
			}
			for _, tc := range []struct{ column, name string }{{"missing", "missing"}, {`"CTID"`, "CTID"}} {
				s := referenceImplicitError("ALTER TABLE t ALTER "+tc.column+" DROP NOT NULL", referenceDiagnostic{Code: "42703", Message: `column "` + tc.name + `" of relation "t" does not exist`})
				s.errorStage = "execute"
				run(s)
			}
			run(referenceExec("ALTER TABLE t ALTER oid DROP NOT NULL", 0))
			run(referenceImplicitError("UPDATE t SET a=NULL", referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, 9).", Schema: "public", Table: "t", Column: "a"}))
			run(referenceSelect("SELECT * FROM t", []string{"a", "oid"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
		})
	}
}

func TestReferenceNotNullSystemColumnSetAddBoundaries(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE t(a INT)", 0))
			for _, backend := range p.backends() {
				for _, tc := range []struct{ q, native, postgres string }{
					{"ALTER TABLE t ALTER ctid SET NOT NULL", "system-column nullability is unsupported", `cannot alter system column "ctid"`},
					{"ALTER TABLE t ADD NOT NULL ctid", `cannot add not-null constraint on system column "ctid"`, `cannot add not-null constraint on system column "ctid"`},
				} {
					message := tc.native
					if backend.pg {
						message = tc.postgres
					}
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: message})
					s.prepared, s.errorStage = prepared, "execute"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
		})
	}
}
