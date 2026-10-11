package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateCoreRecursivePreflightRows(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, table := range []string{"empty", "rejected"} {
		p.Step(t, fixture.Exec("CREATE TABLE "+table+"(a INT,f BOOL,n BIGINT,s TEXT)", 0))
	}
	p.Step(t, fixture.Exec("INSERT INTO rejected VALUES(1,TRUE,2147483648,'oops')", 1))
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			// Preserve the independently observed boundary: PostgreSQL simplifies this
			// FALSE predicate; native logical evaluation makes no short-circuit promise.
			// Both still skip row-dependent RHS casts once WHERE rejects a row.
			for _, table := range []string{"empty", "rejected"} {
				for _, clause := range []string{"SET a=CAST(n AS INTEGER) WHERE FALSE", "SET f=int4(s) IS NULL WHERE FALSE", "SET a=1 WHERE FALSE AND CAST(n AS INTEGER)=0"} {
					q := "UPDATE " + table + " " + clause
					for _, prepared := range []bool{false, true} {
						step := fixture.Exec(q, 0)
						if !backend.PG && table == "rejected" && strings.Contains(clause, "FALSE AND") {
							step = fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "integer out of range"})
						}
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
					}
				}
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SELECT a,f,n,s FROM rejected", []string{"a", "f", "n", "s"}, []string{"INT4", "BOOL", "INT8", "TEXT"}, [][]any{{int64(1), true, int64(2147483648), "oops"}}))
		})
	}
}
