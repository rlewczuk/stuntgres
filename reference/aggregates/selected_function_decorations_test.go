package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Syntax expectations are independent of binding: malformed decoration order
// fails at the second FILTER even when a predicate would fail analysis.
func TestReferenceSelectedFunctionFilterSyntax(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE selected_filter_sentinel(v integer)", 0))
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("INSERT INTO selected_filter_sentinel VALUES(1)", 1))
			for _, tc := range []struct{ query, position string }{
				{"SELECT num_nulls(1) FILTER (WHERE true) FILTER (WHERE false)", "41"},
				{"SELECT num_nulls(1) FILTER (WHERE true) FILTER (WHERE missing)", "41"},
				{"SELECT num_nulls(1) OVER () FILTER (WHERE true)", "29"},
				{"SELECT num_nulls(1) OVER () FILTER (WHERE missing)", "29"},
			} {
				for _, prepared := range []bool{false, true} {
					step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "FILTER"`, Position: tc.position})
					step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
					recovery := fixture.Select("SELECT num_nulls(NULL,1) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}})
					recovery.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, recovery)
				}
			}
			const prefix = "INSERT INTO selected_filter_sentinel VALUES(2); "
			const malformed = "SELECT num_nulls(1) FILTER (WHERE true) FILTER (WHERE false)"
			for _, prepared := range []bool{false, true} {
				for _, queryRows := range []bool{false, true} {
					step := fixture.ImplicitError(prefix+malformed, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "FILTER"`, Position: fixture.Position(len(prefix) + 41)})
					step.Prepared, step.SelectRows, step.ErrorStage = prepared, queryRows, "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT v FROM selected_filter_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				}
			}
		})
	}
}
