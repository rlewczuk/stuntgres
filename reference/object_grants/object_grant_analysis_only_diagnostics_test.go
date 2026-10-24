package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantAnalysisOnlyDiagnostics(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE admission_input(id INT,n INT)", 0))
		run(fixture.Exec("INSERT INTO admission_input VALUES(1,-7),(2,-3)", 2))
		run(fixture.Exec("CREATE TABLE parameter_source(id INT,v TEXT)", 0))
		run(fixture.Exec("GRANT SELECT ON admission_input,parameter_source TO og_object_reader", 0))
		const functionHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		const operatorHint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		cases := []struct {
			query, code, message, marker, hint string
			args                              []any
		}{
			{"WITH c AS (SELECT * FROM absent) SELECT 7", "42P01", `relation "absent" does not exist`, "absent", "", nil},
			{"WITH c AS (SELECT missing) SELECT 7", "42703", `column "missing" does not exist`, "missing", "", nil},
			{"SELECT id FROM admission_input WINDOW w AS (missing ORDER BY missing_k)", "42704", `window "missing" does not exist`, "(missing", "", nil},
			{"SELECT id FROM admission_input WINDOW w AS (PARTITION BY missing_p ORDER BY missing_k)", "42703", `column "missing_k" does not exist`, "missing_k", "", nil},
			{"SELECT id FROM admission_input WINDOW w AS (ORDER BY pg_catalog.abs(TRUE))", "42883", "function pg_catalog.abs(boolean) does not exist", "pg_catalog.abs", functionHint, nil},
			{"WITH c AS (SELECT pg_catalog.abs(TRUE)) SELECT 7", "42883", "function pg_catalog.abs(boolean) does not exist", "pg_catalog.abs", functionHint, nil},
			{"WITH a AS (SELECT id FROM parameter_source WHERE id=$1), b AS (SELECT v FROM parameter_source WHERE v=$1) SELECT * FROM a", "42883", "operator does not exist: text = integer", "=$1) SELECT", operatorHint, []any{int64(1)}},
			{"WITH c AS (SELECT CASE WHEN FALSE THEN missing ELSE n END AS v FROM admission_input) SELECT v FROM c", "42703", `column "missing" does not exist`, "missing", "", nil},
			{"WITH c AS (SELECT CASE WHEN FALSE THEN pg_catalog.abs(TRUE) ELSE n END AS v FROM admission_input) SELECT v FROM c", "42883", "function pg_catalog.abs(boolean) does not exist", "pg_catalog.abs", functionHint, nil},
		}
		for _, revoked := range []bool{false, true} {
			if revoked {
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
			}
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				for _, tc := range cases {
					for _, prefix := range []string{"", "/*é*/ "} {
						q := prefix + tc.query
						pos := utf8.RuneCountInString(q[:strings.Index(q, tc.marker)]) + 1
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: fixture.Position(pos)}, tc.args...)
						s.SelectRows, s.ErrorStage = true, "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				}
			})
		}
	})
}
