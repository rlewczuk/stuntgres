package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceUpdateCoreRecursivePreflightRows(t *testing.T) {
	p := newReferenceScenario(t)
	for _, table := range []string{"empty", "rejected"} {
		p.step(t, referenceExec("CREATE TABLE "+table+"(a INT,f BOOL,n BIGINT,s TEXT)", 0))
	}
	p.step(t, referenceExec("INSERT INTO rejected VALUES(1,TRUE,2147483648,'oops')", 1))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			// Preserve the independently observed boundary: PostgreSQL simplifies this
			// FALSE predicate; native logical evaluation makes no short-circuit promise.
			// Both still skip row-dependent RHS casts once WHERE rejects a row.
			for _, table := range []string{"empty", "rejected"} {
				for _, clause := range []string{"SET a=CAST(n AS INTEGER) WHERE FALSE", "SET f=int4(s) IS NULL WHERE FALSE", "SET a=1 WHERE FALSE AND CAST(n AS INTEGER)=0"} {
					q := "UPDATE " + table + " " + clause
					for _, prepared := range []bool{false, true} {
						step := referenceExec(q, 0)
						if !backend.pg && table == "rejected" && strings.Contains(clause, "FALSE AND") {
							step = referenceImplicitError(q, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
						}
						step.prepared = prepared
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, step)
					}
				}
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceSelect("SELECT a,f,n,s FROM rejected", []string{"a", "f", "n", "s"}, []string{"INT4", "BOOL", "INT8", "TEXT"}, [][]any{{int64(1), true, int64(2147483648), "oops"}}))
		})
	}
}
