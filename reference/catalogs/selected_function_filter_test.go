package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Ordinary calls retain their decoration diagnostics even inside cast and
// operator operands. Child analysis precedes provider execution, STRICT folding
// and catalog demand.
func TestReferenceSelectedFunctionFilterDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, call := range []string{
				"pi()", "abs(1)",
				"random()", "random_normal()", "setseed(0.5)",
				"pg_table_is_visible(0::oid)", "pg_collation_actual_version(0::oid)",
				"col_description(0::oid,1)", "obj_description(0::oid)", "shobj_description(0::oid,'pg_class')",
				"pg_get_viewdef(0::oid)",
				"pg_catalog.overlaps(TIME '01:00',TIME '02:00',TIME '01:30',TIME '03:00')",
			} {
				name := call[:strings.IndexByte(call, '(')]
				for _, predicate := range []string{"false", "true", "missing", "1"} {
					query := "SELECT " + call + " FILTER (WHERE " + predicate + ")"
					want := fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but " + name + " is not an aggregate function", Position: "8"}
					position := fixture.Position(strings.LastIndex(query, predicate) + 1)
					switch predicate {
					case "missing":
						want = fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: position}
					case "1":
						want = fixture.Diagnostic{Code: "42804", Message: "argument of FILTER must be type boolean, not type integer", Position: position}
					}
					for _, prepared := range []bool{false, true} {
						step := fixture.ImplicitError(query, want)
						step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
						if prepared {
							step.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
						recovery := fixture.Select("SELECT pg_encoding_to_char(6) AS encoding", []string{"encoding"}, []string{"NAME"}, [][]any{{"UTF8"}})
						recovery.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, recovery)
					}
				}
			}
			for _, tc := range []struct{ query, message string }{
				{"SELECT pi(*)", "pi(*) specified, but pi is not an aggregate function"},
				{"SELECT random(*)", "random(*) specified, but random is not an aggregate function"},
				{"SELECT random_normal(*)", "random_normal(*) specified, but random_normal is not an aggregate function"},
				{"SELECT pi() OVER ()", "OVER specified, but pi is not a window function nor an aggregate function"},
				{"SELECT random() OVER ()", "OVER specified, but random is not a window function nor an aggregate function"},
				{"SELECT pg_table_is_visible(0::oid) OVER ()", "OVER specified, but pg_table_is_visible is not a window function nor an aggregate function"},
				{"SELECT obj_description(0::oid) OVER ()", "OVER specified, but obj_description is not a window function nor an aggregate function"},
				{"SELECT pg_get_viewdef(0::oid) OVER ()", "OVER specified, but pg_get_viewdef is not a window function nor an aggregate function"},
				{"SELECT pg_catalog.overlaps(TIME '01:00',TIME '02:00',TIME '01:30',TIME '03:00') OVER ()", "OVER specified, but pg_catalog.overlaps is not a window function nor an aggregate function"},
			} {
				for _, prepared := range []bool{false, true} {
					step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42809", Message: tc.message, Position: "8"})
					step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
					recovery := fixture.Select("SELECT pg_encoding_to_char(6) AS encoding", []string{"encoding"}, []string{"NAME"}, [][]any{{"UTF8"}})
					recovery.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, recovery)
				}
			}
			for _, tc := range []struct {
				query string
				want  fixture.Diagnostic
			}{
				{"SELECT (pi() FILTER (WHERE true))::text", fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but pi is not an aggregate function", Position: "9"}},
				{"SELECT 1+abs(1) OVER ()", fixture.Diagnostic{Code: "42809", Message: "OVER specified, but abs is not a window function nor an aggregate function", Position: "10"}},
				{"SELECT (pi() FILTER (WHERE missing))::text", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.Position(strings.Index("SELECT (pi() FILTER (WHERE missing))::text", "missing") + 1)}},
				{"SELECT 1+abs(1) FILTER (WHERE 1)", fixture.Diagnostic{Code: "42804", Message: "argument of FILTER must be type boolean, not type integer", Position: fixture.Position(strings.LastIndex("SELECT 1+abs(1) FILTER (WHERE 1)", "1") + 1)}},
				{"SELECT pg_get_viewdef_extra(1)::text", fixture.Diagnostic{Code: "42883", Message: "function pg_get_viewdef_extra(integer) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}},
			} {
				for _, prepared := range []bool{false, true} {
					step := fixture.ImplicitError(tc.query, tc.want)
					step.SelectRows, step.Prepared, step.ErrorStage = true, prepared, "execute"
					if prepared {
						step.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
					recovery := fixture.Select("SELECT pg_encoding_to_char(6) AS encoding", []string{"encoding"}, []string{"NAME"}, [][]any{{"UTF8"}})
					recovery.Prepared = prepared
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, recovery)
				}
			}
		})
	}
}
