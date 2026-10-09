package reference_test

import (
	"strconv"
	"testing"
)

func TestReferenceUpdateReturningCountCoexistence(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1),(NULL),(2)", 3))
	counts := referenceSelect("SELECT count(*),count(id),count(NULL) FROM t", []string{"count", "count", "count"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3), int64(2), int64(0)}})
	p.step(t, counts)
	p.step(t, referencePrepared(counts))
	hint := "No function matches the given name and argument types. You might need to add explicit type casts."
	for _, tc := range []struct {
		output string
		d      referenceDiagnostic
	}{
		{"count(*)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count(id)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count(NULL)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING"}},
		{"count()", referenceDiagnostic{Code: "42809", Message: "count(*) must be used to call a parameterless aggregate function"}},
		{"count(id,id)", referenceDiagnostic{Code: "42883", Message: "function count(integer, integer) does not exist", Hint: hint}},
		{"count(zzzzz)", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`}},
	} {
		for _, command := range []struct {
			sql      string
			position int
		}{{"UPDATE t SET id=2 RETURNING ", 29}, {"DELETE FROM t RETURNING ", 25}} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + command.sql + tc.output
				d := tc.d
				position := command.position
				if tc.output == "count(zzzzz)" {
					position += 6
				}
				if prefix != "" {
					position += 6
				}
				d.Position = strconv.Itoa(position)
				p.step(t, referenceImplicitError(q, d))
				queryError := referenceImplicitError(q, d)
				queryError.selectRows = true
				p.step(t, queryError)
				p.step(t, referencePrepared(referenceImplicitError(q, d)))
			}
		}
	}
	final := referenceSelect("SELECT id FROM t", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}, {int64(2)}})
	final.bag = true
	p.step(t, final)
}
