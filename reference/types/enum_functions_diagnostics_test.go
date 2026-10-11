package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceEnumFunctionsDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"enum_function_reader"})
	enumFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct {
			query, code, message, hint, stage, at, context string
			args                                           []any
		}{
			{"SELECT enum_first(NULL)", "42883", "function enum_first(unknown) does not exist", hint, "prepare", "enum_first", "", nil},
			{"SELECT enum_last('a')", "42883", "function enum_last(unknown) does not exist", hint, "prepare", "enum_last", "", nil},
			{"SELECT enum_range(NULL,NULL)", "42883", "function enum_range(unknown, unknown) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_first($1)", "42883", "function enum_first(unknown) does not exist", hint, "prepare", "enum_first", "", []any{nil}},
			{"SELECT enum_first(1)", "42883", "function enum_first(integer) does not exist", hint, "prepare", "enum_first", "", nil},
			{"SELECT enum_last('a'::text)", "42883", "function enum_last(text) does not exist", hint, "prepare", "enum_last", "", nil},
			{"SELECT enum_first(NULL::mood_domain)", "42883", "function enum_first(mood_domain) does not exist", hint, "prepare", "enum_first", "", nil},
			{"SELECT enum_range(NULL::mood_domain,NULL::mood)", "42883", "function enum_range(mood_domain, mood) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_range('a'::mood,'a'::other_mood)", "42883", "function enum_range(mood, other_mood) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_range(NULL::mood,NULL::other_mood)", "42883", "function enum_range(mood, other_mood) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT /*é*/ enum_range(NULL::mood,NULL::other_mood)", "42883", "function enum_range(mood, other_mood) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_range(lo,other) FROM enum_endpoints WHERE id=99", "42883", "function enum_range(mood, other_mood) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_first()", "42883", "function enum_first() does not exist", hint, "prepare", "enum_first", "", nil},
			{"SELECT enum_last(NULL::mood,NULL::mood)", "42883", "function enum_last(mood, mood) does not exist", hint, "prepare", "enum_last", "", nil},
			{"SELECT enum_range(NULL::mood,NULL::mood,NULL::mood)", "42883", "function enum_range(mood, mood, mood) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_first(x => NULL::mood)", "42883", "function enum_first(x => mood) does not exist", hint, "prepare", "enum_first", "", nil},
			{"SELECT \"ENUM_FIRST\"(NULL::mood)", "42883", "function ENUM_FIRST(mood) does not exist", hint, "prepare", "\"ENUM_FIRST\"", "", nil},
			{"SELECT enum_first(DISTINCT NULL::mood)", "42809", "DISTINCT specified, but enum_first is not an aggregate function", "", "prepare", "enum_first", "", nil},
			{"SELECT enum_first(NULL::empty_enum)", "55000", "enum empty_enum contains no values", "", "execute", "", "", nil},
			{"SELECT enum_last(NULL::empty_enum)", "55000", "enum empty_enum contains no values", "", "execute", "", "", nil},
			{"SELECT enum_first('nope'::mood)", "22P02", `invalid input value for enum mood: "nope"`, "", "prepare", "'nope'", "", nil},
			{"SELECT /*é*/ enum_last('nope'::mood)", "22P02", `invalid input value for enum mood: "nope"`, "", "prepare", "'nope'", "", nil},
			{"SELECT enum_range(NULL::mood,1)", "42883", "function enum_range(mood, integer) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_range(NULL,NULL) FROM enum_endpoints WHERE id=99", "42883", "function enum_range(unknown, unknown) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_range(NULL::mood,NULL::other_mood) FROM enum_endpoints WHERE id=99", "42883", "function enum_range(mood, other_mood) does not exist", hint, "prepare", "enum_range", "", nil},
			{"SELECT enum_first($1::mood)", "22P02", `invalid input value for enum mood: "nope"`, "", "execute", "", "unnamed portal parameter $1 = '...'", []any{"nope"}},
			{"SELECT absent.enum_first(NULL::mood)", "3F000", `schema "absent" does not exist`, "", "prepare", "absent", "", nil},
		} {
			t.Run(tc.query, func(t *testing.T) {
				want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint, Context: tc.context}
				if tc.at != "" {
					want.Position = strconv.Itoa(utf8.RuneCountInString(tc.query[:strings.Index(tc.query, tc.at)]) + 1)
				}
				fixture.EnumReferenceErrorChecked(t, p, conn, tc.query, true, tc.stage, func(t *testing.T, got fixture.Diagnostic) {
					if got != want {
						t.Errorf("enum diagnostic query=%q got %#v want %#v", tc.query, got, want)
					}
				}, tc.args...)
			})
		}
		for _, prepared := range []bool{false, true} {
			enumFunctionsSelect(t, p, b, conn, "SELECT enum_first(VARIADIC NULL::mood) AS v", "z", false, prepared)
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Select("SELECT id FROM enum_endpoints ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}}))
		enumFunctionsAuthorization(t, p, b, conn)
	})
}
