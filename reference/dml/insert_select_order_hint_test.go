package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
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
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.InsertSelectRun(t, p.Ctx, backend.DB, backend.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(i INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(9)", 1))
					want := fixture.Diagnostic{Code: "42703", Message: `column "` + tc.name + `" does not exist`, Hint: tc.hint, Position: strconv.Itoa(strings.Index(q, tc.key) + 1)}
					for _, prepared := range []bool{false, true} {
						step := fixture.ImplicitError(q, want)
						step.Prepared, step.ErrorStage = prepared, "execute"
						if prepared {
							step.ErrorStage = "prepare"
						}
						run(step)
						run(fixture.Select("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
					}
				})
			}
		})
	}
}
