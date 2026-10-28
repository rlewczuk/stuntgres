package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceArraySearchFilterDiagnostics(t *testing.T) {
	arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const functionHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		var cases []struct {
			name, q string
			d       fixture.Diagnostic
		}
		for _, call := range []struct{ name, args string }{
			{"array_position", "ARRAY[1],1"}, {"array_positions", "ARRAY[1],1"},
			{"array_remove", "ARRAY[1],1"}, {"array_replace", "ARRAY[1],1,2"},
		} {
			for _, filter := range []string{"missing", "1", "TRUE", "NULL"} {
				q := "SELECT " + call.name + "(" + call.args + ") FILTER (WHERE " + filter + ")"
				d := fixture.Diagnostic{Code: "42809", Message: "FILTER specified, but " + call.name + " is not an aggregate function", Position: "8"}
				position := fixture.Position(utf8.RuneCountInString(q[:strings.LastIndex(q, "WHERE ")+6]) + 1)
				if filter == "missing" {
					d = fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: position}
				}
				if filter == "1" {
					d = fixture.Diagnostic{Code: "42804", Message: "argument of FILTER must be type boolean, not type integer", Position: position}
				}
				cases = append(cases, struct {
					name, q string
					d       fixture.Diagnostic
				}{call.name + "_" + filter, q, d})
			}
		}
		for _, tc := range []struct{ name, q, code, message, token string }{
			{"invalid_signature_missing", "SELECT array_position(1,1) FILTER (WHERE missing)", "42703", `column "missing" does not exist`, "missing"},
			{"invalid_signature_nonbool", "SELECT array_position(1,1) FILTER (WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "WHERE 1"},
			{"invalid_signature_bool", "SELECT array_position(1,1) FILTER (WHERE TRUE)", "42883", "function array_position(integer, integer) does not exist", "array_position"},
			{"invalid_arity_missing", "SELECT array_position() FILTER (WHERE missing)", "42703", `column "missing" does not exist`, "missing"},
			{"invalid_arity_nonbool", "SELECT array_replace(ARRAY[1],1) FILTER (WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "WHERE 1"},
			{"ordinary_child_first", "SELECT array_position(ARRAY[1], missing_arg) FILTER (WHERE missing_filter)", "42703", `column "missing_arg" does not exist`, "missing_arg"},
			{"unicode_nonbool", "SELECT /*é*/array_position(1,1) FILTER (WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "WHERE 1"},
		} {
			index := strings.Index(tc.q, tc.token)
			if tc.token == "WHERE 1" {
				index += 6
			}
			d := fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(utf8.RuneCountInString(tc.q[:index]) + 1)}
			if tc.code == "42883" {
				d.Hint = functionHint
			}
			cases = append(cases, struct {
				name, q string
				d       fixture.Diagnostic
			}{tc.name, tc.q, d})
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				arraySearchDiagnosticBoth(t, p, b, c, tc.q, tc.d, true)
			})
		}
	})
}

func arraySearchReviewViewDefinition(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want string) {
	t.Helper()
	const q = "SELECT pg_get_viewdef('as_review_view'::regclass,true) AS definition"
	arraySearchBoth(t, p, b, c, fixture.Select(q, []string{"definition"}, []string{"TEXT"}, [][]any{{want}}))
}
