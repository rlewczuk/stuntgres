package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Complete fields, positions and stages were observed on pinned PostgreSQL 18.6.
// Unset Diagnostic fields are required to remain empty.
func TestReferenceStatisticsMCVItemsDiagnostics(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		const parameterQuery = "SELECT * FROM pg_mcv_list_items($1::pg_mcv_list)"
		empty := func() fixture.Step {
			step := fixture.Select(parameterQuery, mcvItemsNames, mcvItemsTypes, nil, nil)
			step.CheckMetadata = mcvItemsMetadata(s.pg)
			return step
		}
		for _, c := range []struct {
			query, code, message, hint string
			position                   int
		}{
			{"SELECT * FROM pg_mcv_list_items('x'::pg_mcv_list)", "0A000", "cannot accept a value of type pg_mcv_list", "", 33},
			{"SELECT * FROM pg_mcv_list_items(1)", "42883", "function pg_mcv_list_items(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", 15},
			{"SELECT * FROM pg_mcv_list_items()", "42883", "function pg_mcv_list_items() does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", 15},
			{"SELECT * FROM pg_mcv_list_items(NULL::pg_mcv_list,NULL::pg_mcv_list)", "42883", "function pg_mcv_list_items(pg_mcv_list, pg_mcv_list) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", 15},
			{"SELECT * FROM pg_mcv_list_items(wrong => NULL::pg_mcv_list)", "42883", "function pg_mcv_list_items(wrong => pg_mcv_list) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", 15},
			{"SELECT * FROM pg_mcv_list_items(NULL::pg_mcv_list) AS m(i integer)", "42601", "a column definition list is redundant for a function with OUT parameters", "", 57},
			{"SELECT * FROM pg_mcv_list_items(mcv_list => NULL::pg_mcv_list,mcv_list => NULL::pg_mcv_list)", "42601", `argument name "mcv_list" used more than once`, "", 63},
			{"SELECT * FROM pg_mcv_list_items(VARIADIC ARRAY[NULL::pg_mcv_list])", "42704", "could not find array type for data type pg_mcv_list", "", 42},
			{"SELECT * FROM pg_mcv_list_items(DISTINCT NULL::pg_mcv_list)", "42809", "DISTINCT specified, but pg_mcv_list_items is not an aggregate function", "", 15},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				d := fixture.ControlsReferenceDiagnostic(c.code, c.message)
				d.Hint = c.hint
				d.Position = fixture.Position(c.position + utf8.RuneCountInString(prefix))
				step := fixture.ErrorDiagnostic(prefix+c.query, d)
				step.SelectRows = true
				step.ErrorStage = "execute"
				if s.prepared {
					step.ErrorStage = "prepare"
				}
				s.run(step)
				s.run(empty())
			}
		}
		for _, held := range []bool{false, true} {
			run := s.run
			if held {
				stmt, err := s.conn.PrepareContext(s.pair.Ctx, parameterQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				recorder := s.pair.MemoryRecord
				if s.pg {
					recorder = s.pair.PostgresRecord
				}
				executor := fixture.HeldStatement{Executor: s.conn, Statement: stmt, SQL: parameterQuery}
				run = func(step fixture.Step) {
					fixture.RunStep(t, s.pair.Ctx, executor, recorder, step)
				}
			}
			// Each failure is followed by NULL on the same physical owner and,
			// in the held phase, on the very same prepared statement.
			for _, arg := range []any{nil, "x", nil, []byte("x"), nil, "", nil} {
				if arg == nil {
					run(empty())
					continue
				}
				d := fixture.ControlsReferenceDiagnostic("0A000", "cannot accept a value of type pg_mcv_list")
				d.Context = "unnamed portal parameter $1 = '...'"
				if text, ok := arg.(string); ok && text == "" {
					d.Context = "unnamed portal parameter $1 = ''"
				}
				step := fixture.ErrorDiagnostic(parameterQuery, d, arg)
				step.SelectRows = true
				step.ErrorStage = "execute"
				run(step)
			}
		}
	})
}
