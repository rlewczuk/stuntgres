package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL18 discovery pinned compound modifier diagnostics at CREATE
// execution, while parentheses around a simple constant retain its width.
func TestReferenceBitstringModifierExpressions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		for _, family := range []string{"BIT", "VARBIT", "BIT VARYING", `"bit"`, "pg_catalog.varbit"} {
			for _, modifier := range []string{"1-1", "1*2", "1/2", "(1+1)", "(1)", "1-(1)"} {
				q := "CREATE TABLE bs_modifier_expr(v " + family + "(" + modifier + "))"
				for _, prepared := range []bool{false, true} {
					t.Run(backend.Name+"/"+family+"/"+modifier+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						if modifier != "(1)" {
							bitstringReferenceError(t, p, backend, backend.DB, q, nil, prepared, false, "42601", "type modifiers must be simple constants or identifiers", "33")
							return
						}
						create := fixture.Exec(q, 0)
						create.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, create)
						databaseType := "VARBIT"
						if family == "BIT" || family == `"bit"` {
							databaseType = "BIT"
						}
						check := func(want [][]any) {
							s := fixture.Select("SELECT v FROM bs_modifier_expr", []string{"v"}, []string{databaseType}, want)
							s.Prepared, s.Bag = prepared, true
							s.CheckMetadata = bitstringReferenceMetadata(backend.PG, []int64{1}, []bool{true})
							fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
						}
						check(nil)
						insert := fixture.Exec("INSERT INTO bs_modifier_expr VALUES(B'1'),(NULL)", 2)
						insert.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, insert)
						check([][]any{{"1"}, {nil}})
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("DROP TABLE bs_modifier_expr", 0))
					})
				}
			}
		}
	}
}
