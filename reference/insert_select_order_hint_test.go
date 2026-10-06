//go:build reference

package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

func TestReferenceInsertSelectValuesOrderSuggestions(t *testing.T) {
	for _, tc := range []struct{ values, key, name, hint string }{
		{"(1)", "column2", "column2", `Perhaps you meant to reference the column "*VALUES*.column1".`},
		{"(1,2)", "column3", "column3", `Perhaps you meant to reference the column "*VALUES*.column1" or the column "*VALUES*.column2".`},
		{"(1,2,3)", "column4", "column4", ""},
		{"(1)", "zzzzzzz", "zzzzzzz", ""},
		{"(1,2,3,4,5,6,7,8,9,10)", "column99", "column99", `Perhaps you meant to reference the column "*VALUES*.column9".`},
		{"(1)", `"cólumn2"`, "cólumn2", `Perhaps you meant to reference the column "*VALUES*.column1".`},
		{"(1)", `"Column2"`, "Column2", `Perhaps you meant to reference the column "*VALUES*.column1".`},
	} {
		q := "INSERT INTO t VALUES" + tc.values + " ORDER BY " + tc.key
		t.Run(q, func(t *testing.T) {
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE TABLE t(i INT)", 0))
			p.step(t, referenceExec("INSERT INTO t VALUES(9)", 1))
			want := referenceDiagnostic{Code: "42703", Message: `column "` + tc.name + `" does not exist`, Hint: tc.hint, Position: strconv.Itoa(strings.Index(q, tc.key) + 1)}
			for _, prepared := range []bool{false, true} {
				step := referenceImplicitError(q, want)
				step.prepared = prepared
				step.errorStage = "execute"
				if prepared {
					step.errorStage = "prepare"
				}
				p.step(t, step)
			}
			p.step(t, referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
		})
	}
}
