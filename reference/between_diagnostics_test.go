//go:build reference

package reference_test

import "testing"

func TestReferenceBetweenDiagnostics(t *testing.T) {
	const hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "populated", true: "empty"}[empty], func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(i INTEGER)", 0))
			if !empty {
				p.step(t, referenceExec("INSERT INTO t VALUES(NULL),(0)", 2))
			}
			for _, tc := range []struct {
				q    string
				d    referenceDiagnostic
				args []any
			}{
				{"SELECT i BETWEEN TRUE AND 3 FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer >= boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i BETWEEN 1 AND TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer <= boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i NOT BETWEEN TRUE AND 3 FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer < boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i NOT BETWEEN 1 AND TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer > boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i BETWEEN SYMMETRIC 1 AND TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer <= boolean", Position: "10", Hint: hint}, nil},
				{"SELECT i NOT BETWEEN SYMMETRIC 1 AND TRUE FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer > boolean", Position: "10", Hint: hint}, nil},
				{"SELECT /*é*/ i BETWEEN TRUE AND 3 FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer >= boolean", Position: "16", Hint: hint}, nil},
				{"SELECT i BETWEEN 'oops' AND 3 FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "18"}, nil},
				{"SELECT i BETWEEN '2147483648' AND 3 FROM t", referenceDiagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "18"}, nil},
				{"SELECT i BETWEEN 1 AND 'oops' FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`, Position: "24"}, nil},
				{"SELECT $1,i BETWEEN $1 AND 3 FROM t", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}, []any{2}},
				{"SELECT i FROM t WHERE i BETWEEN 1 AND TRUE", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer <= boolean", Position: "25", Hint: hint}, nil},
			} {
				step := referenceImplicitError(tc.q, tc.d, tc.args...)
				step.selectRows = true
				step.errorStage = "execute"
				p.step(t, step)
				step.errorStage = "prepare"
				p.step(t, referencePrepared(step))
			}
			bad := referenceImplicitError("SELECT i BETWEEN $1 AND $2 FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}, "oops", 3)
			bad.selectRows = true
			bad.errorStage = "execute"
			pg := *bad.diagnostic
			pg.Context = "unnamed portal parameter $1 = '...'"
			bad.postgresDiagnostic = &pg
			betweenReferenceBoth(t, p, bad)
		})
	}
}
