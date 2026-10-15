package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordUnresolvedExpansion(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_expansion_zero AS ()`)
		// A real zero-member named shape and an occurrence registered by ROW()
		// must not be confused with a function's unresolved RECORD result.
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record(NULL::jbr_expansion_zero,'{}'::jsonb)::text AS named,jsonb_populate_record(ROW(),'{}'::jsonb)::text AS registered`, []string{"named", "registered"}, []string{"TEXT", "TEXT"}, [][]any{{"()", "()"}}))
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_to_record(NULL::jsonb) IS NULL AS strict_null`, []string{"strict_null"}, []string{"BOOL"}, [][]any{{true}}))
		for _, tc := range []struct{ name, arguments string }{
			{"jsonb_to_record", `'{}'::jsonb`},
			{"jsonb_to_record", `NULL::jsonb`},
			{"jsonb_to_recordset", `'[]'::jsonb`},
			{"jsonb_to_recordset", `NULL::jsonb`},
			{"jsonb_populate_record", `NULL::record,'{}'::jsonb`},
			{"jsonb_populate_record", `NULL::record,NULL::jsonb`},
			{"jsonb_populate_recordset", `NULL::record,'[]'::jsonb`},
			{"jsonb_populate_recordset", `NULL::record,NULL::jsonb`},
		} {
			t.Run(tc.name+"/"+tc.arguments, func(t *testing.T) {
				// parse_target.c -> get_expr_result_tupdesc(...,false), rather
				// than a fabricated empty layout or the scalar runtime error.
				d := fixture.ControlsReferenceDiagnostic("42809", "record type has not been registered")
				fixture.CompositeReferenceError(t, p, c, `SELECT (`+tc.name+`(`+tc.arguments+`)).*`, "prepare", d)
			})
		}
		for _, tc := range []struct{ name, arguments string }{
			{"jsonb_to_record", `'{}'::jsonb`},
			{"jsonb_to_recordset", `'[]'::jsonb`},
			{"jsonb_populate_record", `NULL::record,'{}'::jsonb`},
			{"jsonb_populate_recordset", `NULL::record,'[]'::jsonb`},
		} {
			t.Run("scalar/"+tc.name, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("0A000", "could not determine row type for result of "+tc.name)
				d.Hint = jsonbRecordRowHint
				fixture.CompositeReferenceError(t, p, c, `SELECT `+tc.name+`(`+tc.arguments+`)`, "execute", d)
			})
		}
	})
}
