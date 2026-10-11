package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualInsertSelectConstraints(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name, recipe, input string
				value               any
				rows                [][]any
				want                *fixture.Diagnostic
			}{
				{name: "non_null", recipe: "a+1", input: "2", value: 2, rows: [][]any{{int64(2), int64(3)}}},
				{name: "constant_null_owner", recipe: "9", input: "NULL::INT", rows: [][]any{{nil, int64(9)}}},
				{name: "non_strict_null_owner", recipe: "COALESCE(a,9)", input: "NULL::INT", rows: [][]any{{nil, int64(9)}}},
				{name: "logical_null", recipe: "a+1", input: "NULL::INT", want: &fixture.Diagnostic{Code: "23502", Message: `null value in column "b" of relation "virtual_insert_nn" violates not-null constraint`, Detail: "Failing row contains (null, virtual).", Schema: "public", Table: "virtual_insert_nn", Column: "b"}},
				{name: "generation_error", recipe: "1/a", input: "0", value: 0, want: &fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec(`CREATE TABLE virtual_insert_nn(a INT,b INT GENERATED ALWAYS AS(`+tc.recipe+`) VIRTUAL NOT NULL)`, 0))
						query := `INSERT INTO virtual_insert_nn(a) SELECT ` + tc.input
						var args []any
						if prepared {
							query = `INSERT INTO virtual_insert_nn(a) SELECT $1::INT`
							args = []any{tc.value}
						}
						step := fixture.Exec(query, 1, args...)
						if tc.want != nil {
							step = fixture.ImplicitError(query, *tc.want, args...)
							step.ErrorStage = "execute"
						}
						step.Prepared = prepared
						run(step)
						run(fixture.Select(`SELECT a,b FROM virtual_insert_nn`, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows))
					})
				})
			}
			t.Run("nullable_remains_lazy", func(t *testing.T) {
				generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec(`CREATE TABLE virtual_insert_lazy(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL)`, 0))
					step := fixture.Exec(`INSERT INTO virtual_insert_lazy(a) SELECT 0`, 1)
					step.Prepared = prepared
					run(step)
					run(fixture.Select(`SELECT a FROM virtual_insert_lazy`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
					demand := fixture.ImplicitError(`SELECT b FROM virtual_insert_lazy`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
					demand.Prepared = prepared
					demand.ErrorStage = "execute"
					run(demand)
					run(fixture.Select(`SELECT a FROM virtual_insert_lazy`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
				})
			})
		})
	}
}

func TestReferenceGeneratedVirtualInsertSelectAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name, recipe, source string
				rows                 [][]any
				want                 fixture.Diagnostic
			}{
				{name: "late_logical_null", recipe: "a+1", source: `(1,4),(2,NULL)`, rows: [][]any{{int64(2), int64(3)}, {int64(3), int64(4)}}, want: fixture.Diagnostic{Code: "23502", Message: `null value in column "b" of relation "virtual_insert_atomic" violates not-null constraint`, Detail: "Failing row contains (null, virtual).", Schema: "public", Table: "virtual_insert_atomic", Column: "b"}},
				{name: "late_generation_error", recipe: "1/a", source: `(1,4),(2,0)`, rows: [][]any{{int64(2), int64(0)}, {int64(3), int64(0)}}, want: fixture.Diagnostic{Code: "22012", Message: "division by zero"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec(`CREATE TABLE virtual_insert_atomic(a INT,b INT GENERATED ALWAYS AS(`+tc.recipe+`) VIRTUAL NOT NULL)`, 0))
						run(fixture.Exec(`INSERT INTO virtual_insert_atomic(a) VALUES(2),(3)`, 2))
						run(fixture.Exec(`CREATE TABLE virtual_insert_source(id INT,a INT)`, 0))
						run(fixture.Exec(`INSERT INTO virtual_insert_source VALUES`+tc.source, 2))
						step := fixture.ImplicitError(`INSERT INTO virtual_insert_atomic(a) SELECT a FROM virtual_insert_source ORDER BY id`, tc.want)
						step.Prepared = prepared
						step.ErrorStage = "execute"
						run(step)
						run(fixture.Select(`SELECT a,b FROM virtual_insert_atomic ORDER BY a`, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.rows))
					})
				})
			}
		})
	}
}
