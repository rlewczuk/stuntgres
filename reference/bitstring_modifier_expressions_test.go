package reference_test

import "testing"

// PostgreSQL18 discovery pinned compound modifier diagnostics at CREATE
// execution, while parentheses around a simple constant retain its width.
func TestReferenceBitstringModifierExpressions(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		for _, family := range []string{"BIT", "VARBIT", "BIT VARYING", `"bit"`, "pg_catalog.varbit"} {
			for _, modifier := range []string{"1-1", "1*2", "1/2", "(1+1)", "(1)", "1-(1)"} {
				q := "CREATE TABLE bs_modifier_expr(v " + family + "(" + modifier + "))"
				for _, prepared := range []bool{false, true} {
					t.Run(backend.name+"/"+family+"/"+modifier+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
						if modifier != "(1)" {
							bitstringReferenceError(t, p, backend, backend.db, q, nil, prepared, false, "42601", "type modifiers must be simple constants or identifiers", "33")
							return
						}
						create := referenceExec(q, 0)
						create.prepared = prepared
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, create)
						databaseType := "VARBIT"
						if family == "BIT" || family == `"bit"` {
							databaseType = "BIT"
						}
						check := func(want [][]any) {
							s := referenceSelect("SELECT v FROM bs_modifier_expr", []string{"v"}, []string{databaseType}, want)
							s.prepared, s.bag = prepared, true
							s.checkMetadata = bitstringReferenceMetadata(backend.pg, []int64{1}, []bool{true})
							referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
						}
						check(nil)
						insert := referenceExec("INSERT INTO bs_modifier_expr VALUES(B'1'),(NULL)", 2)
						insert.prepared = prepared
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, insert)
						check([][]any{{"1"}, {nil}})
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("DROP TABLE bs_modifier_expr", 0))
					})
				}
			}
		}
	}
}
