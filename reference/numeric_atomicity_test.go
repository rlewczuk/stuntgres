package reference_test

import (
	"testing"
)

func TestReferenceNumericStatementAtomicity(t *testing.T) {
	for _, shape := range []struct {
		table, id string
		position  string
	}{{"n", "INT NOT NULL", "34"}, {"atomic", "INT", "39"}} {
		t.Run(shape.table, func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE "+shape.table+"(id "+shape.id+",v NUMERIC(3,1) NOT NULL,b BIGINT)", 0))
			p.step(t, referenceExec("INSERT INTO "+shape.table+" VALUES(99,9.9,99)", 1))
			unchanged := referenceSelect("SELECT * FROM "+shape.table, []string{"id", "v", "b"}, []string{"INT4", "NUMERIC", "INT8"}, [][]any{{int64(99), "9.9", int64(99)}})
			for _, tc := range []struct {
				tail string
				want referenceDiagnostic
			}{
				{"'oops',2", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: shape.position}},
				{"99.95,2", referenceDiagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}},
				{"1.2,9223372036854775808", referenceDiagnostic{Code: "22003", Message: "bigint out of range"}},
				{"NULL,2", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "` + shape.table + `" violates not-null constraint`, Schema: "public", Table: shape.table, Column: "v", Detail: "Failing row contains (2, null, 2)."}},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO "+shape.table+" VALUES(1,1.2,1),(2,"+tc.tail+")", tc.want)
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared && tc.want.Code == "22P02" {
						s.errorStage = "prepare"
					}
					p.step(t, s)
					p.step(t, unchanged)
				}
			}
		})
	}
}

func TestReferenceNumericSettingExclusions(t *testing.T) {
	p := newReferenceScenario(t)
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceExec("SET application_name TO 'before'", 0))
	for _, literal := range []string{"1.5", ".5", "1e2"} {
		failure := referenceImplicitError("SET application_name TO "+literal, referenceDiagnostic{Code: "0A000", Message: "non-decimal-integer literal is unsupported", Position: "25"})
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, failure)
		referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"before"}}))
	}
}
