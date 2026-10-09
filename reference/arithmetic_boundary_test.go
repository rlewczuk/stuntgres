package reference_test

import "testing"

func TestReferenceArithmeticBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE x(s SMALLINT,s2 SMALLINT,n NUMERIC,n2 NUMERIC,r REAL,r2 REAL,d DOUBLE PRECISION,d2 DOUBLE PRECISION)", 0))
			run(referenceExec("INSERT INTO x VALUES(7,2,7,2,7,2,7,2)", 1))
			for _, tc := range []struct {
				expr, typ, diagnostic, pos string
				value                      any
			}{
				{"s+s2", "INT2", "", "9", int64(9)}, {"s/s2", "INT2", "", "9", int64(3)}, {"s%s2", "INT2", "", "9", int64(1)}, {"s+2", "INT4", "", "9", int64(9)}, {"+s", "INT2", "", "8", int64(7)}, {"-s", "INT2", "", "8", int64(-7)},
				{"n+n2", "NUMERIC", "numeric", "9", "9"}, {"n*n2", "NUMERIC", "numeric", "9", "14"}, {"n%n2", "NUMERIC", "numeric", "9", "1"},
				{"r/r2", "FLOAT4", "", "9", float64(3.5)}, {"d/d2", "FLOAT8", "", "9", float64(3.5)}, {"+r", "FLOAT4", "", "8", float64(7)}, {"-d", "FLOAT8", "", "8", float64(-7)},
			} {
				q := "SELECT " + tc.expr + " FROM x"
				s := referenceSelect(q, []string{"?column?"}, []string{tc.typ}, [][]any{{tc.value}})
				if !backend.pg && tc.diagnostic != "" {
					s = referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "arithmetic for " + tc.diagnostic + " operands is unsupported", Position: tc.pos})
					s.selectRows = true
				}
				run(s)
				if s.state != "" {
					s.errorStage = "prepare"
				}
				run(referencePrepared(s))
			}
			run(referenceExec("CREATE TABLE t(s SMALLINT,n NUMERIC,r REAL,d DOUBLE PRECISION)", 0))
			run(referenceExec("INSERT INTO t VALUES(7,+1.5,7,7)", 1))
			n := referenceSelect("SELECT n FROM t", []string{"n"}, []string{"NUMERIC"}, [][]any{{"1.5"}})
			run(n)
			run(referencePrepared(n))
			for _, tc := range []struct {
				expr, typ, diagnostic, pos string
				value                      any
			}{{"s+1", "INT4", "", "9", int64(8)}, {"+n", "NUMERIC", "numeric", "8", "1.5"}, {"r/2", "FLOAT8", "", "9", float64(3.5)}, {"d*2", "FLOAT8", "", "9", float64(14)}} {
				q := "SELECT " + tc.expr + " FROM t"
				s := referenceSelect(q, []string{"?column?"}, []string{tc.typ}, [][]any{{tc.value}})
				if !backend.pg && tc.diagnostic != "" {
					s = referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "arithmetic for " + tc.diagnostic + " operands is unsupported", Position: tc.pos})
					s.selectRows = true
				}
				run(s)
				if s.state != "" {
					s.errorStage = "prepare"
				}
				run(referencePrepared(s))
			}
			// Both original source tables and both direct/Prepare routes remain covered.
			for _, table := range []string{"x", "t"} {
				for _, tc := range []struct {
					expr, signature, pos string
					args                 []any
				}{{"-$1", "- unknown", "8", []any{7}}, {"$1+$2", "unknown + unknown", "10", []any{7, 2}}, {"NULL+NULL", "unknown + unknown", "12", nil}, {"$1%$2", "unknown % unknown", "10", []any{7, 2}}} {
					s := referenceImplicitError("SELECT "+tc.expr+" FROM "+table, referenceDiagnostic{Code: "42725", Message: "operator is not unique: " + tc.signature, Position: tc.pos, Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}, tc.args...)
					s.selectRows = true
					run(s)
					s.errorStage = "prepare"
					run(referencePrepared(s))
				}
				for _, arg := range []any{7, nil} {
					var want any = float64(7)
					if arg == nil {
						want = nil
					}
					s := referenceSelect("SELECT +$1 FROM "+table, []string{"?column?"}, []string{"FLOAT8"}, [][]any{{want}}, arg)
					run(s)
					run(referencePrepared(s))
				}
			}
		})
	}
}
