package reference_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Initially source-derived contracts, corrected from the root-recorded pinned
// PostgreSQL18.0 discovery in tests-1.md; parameter values are redacted there.
func TestReferenceCatalogRelationsDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	catalogRelationsBackends(t, p, func(t *testing.T, b fixture.Backend) {
		hint := "No function matches the given name and argument types. You might need to add explicit type casts."
		type sample struct {
			name, sql, code, message, hint, at, context string
			args                                        []any
		}
		cases := []sample{}
		noMatch := func(name, call, signature string) {
			cases = append(cases, sample{name: name, sql: "SELECT " + call, code: "42883", message: "function " + signature + " does not exist", hint: hint, at: call})
		}
		noMatch("scalar_zero", "pg_get_userbyid()", "pg_get_userbyid()")
		noMatch("scalar_two", "pg_get_userbyid(1,2)", "pg_get_userbyid(integer, integer)")
		noMatch("scalar_bool", "pg_get_userbyid(true)", "pg_get_userbyid(boolean)")
		noMatch("scalar_text", "pg_get_userbyid('10'::text)", "pg_get_userbyid(text)")
		noMatch("set_integer", "pg_get_catalog_foreign_keys(1)", "pg_get_catalog_foreign_keys(integer)")
		noMatch("set_null", "pg_get_catalog_foreign_keys(NULL)", "pg_get_catalog_foreign_keys(unknown)")
		noMatch("manual_label", "pg_get_userbyid(role => 10)", "pg_get_userbyid(role => integer)")
		noMatch("wrong_label", "pg_get_userbyid(absent => 10)", "pg_get_userbyid(absent => integer)")
		noMatch("out_is_not_input", "pg_get_catalog_foreign_keys(fktable => 1)", "pg_get_catalog_foreign_keys(fktable => integer)")
		noMatch("wrong_schema", "public.pg_get_userbyid(0)", "public.pg_get_userbyid(integer)")
		noMatch("wrong_set_schema", "public.pg_get_catalog_foreign_keys()", "public.pg_get_catalog_foreign_keys()")
		noMatch("quoted_case", `"PG_GET_USERBYID"(0)`, "PG_GET_USERBYID(integer)")
		noMatch("missing_name", "pg_get_userbyid_missing(0)", "pg_get_userbyid_missing(integer)")
		cases = append(cases,
			sample{name: "unicode_position", sql: "SELECT /* é界 */ pg_get_userbyid(true)", code: "42883", message: "function pg_get_userbyid(boolean) does not exist", hint: hint, at: "pg_get_userbyid"},
			sample{name: "oid_literal", sql: "SELECT pg_get_userbyid('bad')", code: "22P02", message: `invalid input syntax for type oid: "bad"`, at: "'bad'"},
			sample{name: "oid_overflow", sql: "SELECT pg_get_userbyid('4294967296')", code: "22003", message: `value "4294967296" is out of range for type oid`, at: "'4294967296'"},
			sample{name: "oid_parameter", sql: "SELECT pg_get_userbyid($1)", code: "22P02", message: `invalid input syntax for type oid: "bad"`, context: `unnamed portal parameter $1 = '...'`, args: []any{"bad"}},
			sample{name: "overflow_parameter", sql: "SELECT pg_get_userbyid($1)", code: "22003", message: `value "4294967296" is out of range for type oid`, context: `unnamed portal parameter $1 = '...'`, args: []any{"4294967296"}},
			sample{name: "set_star", sql: "SELECT pg_get_catalog_foreign_keys(*)", code: "42809", message: "pg_get_catalog_foreign_keys(*) specified, but pg_get_catalog_foreign_keys is not an aggregate function", at: "pg_get_catalog_foreign_keys"},
			sample{name: "scalar_distinct", sql: "SELECT pg_get_userbyid(DISTINCT 0)", code: "42809", message: "DISTINCT specified, but pg_get_userbyid is not an aggregate function", at: "pg_get_userbyid"},
			sample{name: "scalar_order", sql: "SELECT pg_get_userbyid(0 ORDER BY 0)", code: "42809", message: "ORDER BY specified, but pg_get_userbyid is not an aggregate function", at: "pg_get_userbyid"},
			sample{name: "scalar_filter", sql: "SELECT pg_get_userbyid(0) FILTER (WHERE true)", code: "42809", message: "FILTER specified, but pg_get_userbyid is not an aggregate function", at: "pg_get_userbyid"},
			sample{name: "scalar_over", sql: "SELECT pg_get_userbyid(0) OVER ()", code: "42809", message: "OVER specified, but pg_get_userbyid is not a window function nor an aggregate function", at: "pg_get_userbyid"},
			sample{name: "set_filter", sql: "SELECT pg_get_catalog_foreign_keys() FILTER (WHERE true)", code: "42809", message: "FILTER specified, but pg_get_catalog_foreign_keys is not an aggregate function", at: "pg_get_catalog_foreign_keys"},
			sample{name: "set_over", sql: "SELECT pg_get_catalog_foreign_keys() OVER ()", code: "42809", message: "OVER specified, but pg_get_catalog_foreign_keys is not a window function nor an aggregate function", at: "pg_get_catalog_foreign_keys"},
			sample{name: "where", sql: "SELECT 1 WHERE pg_get_catalog_foreign_keys() IS NOT NULL", code: "0A000", message: "set-returning functions are not allowed in WHERE", at: "pg_get_catalog_foreign_keys"},
			sample{name: "nested_from", sql: "SELECT * FROM generate_series(1,pg_get_catalog_foreign_keys())", code: "42883", message: "function generate_series(integer, record) does not exist", hint: hint, at: "generate_series"},
			sample{name: "too_many_aliases", sql: "SELECT * FROM pg_get_catalog_foreign_keys() AS f(a,b,c,d,e,f,g)", code: "42P10", message: "table \"f\" has 6 columns available but 7 columns specified"},
			sample{name: "bad_argument_before_decoration", sql: "SELECT pg_get_userbyid(DISTINCT true)", code: "42883", message: "function pg_get_userbyid(boolean) does not exist", hint: hint, at: "pg_get_userbyid"},
			sample{name: "bad_argument_before_placement", sql: "SELECT 1 WHERE pg_get_catalog_foreign_keys(1) IS NOT NULL", code: "42883", message: "function pg_get_catalog_foreign_keys(integer) does not exist", hint: hint, at: "pg_get_catalog_foreign_keys"},
		)
		for _, c := range cases {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%t", c.name, prepared), func(t *testing.T) {
					position := ""
					if c.at != "" {
						index := strings.Index(c.sql, c.at)
						if index < 0 {
							t.Fatal("diagnostic source anchor missing", c.at)
						}
						position = fixture.Position(utf8.RuneCountInString(c.sql[:index]) + 1)
					}
					want := fixture.Diagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: position, Context: c.context}
					s := fixture.ImplicitError(c.sql, want, c.args...)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared && len(c.args) == 0 {
						s.ErrorStage = "prepare"
					}
					// PostgreSQL signature resolution precedes placement for nested_from;
					// both direct and prepared parameter failures redact their input Context.
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				})
			}
		}
	})
}
