package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArraySearchDiagnostics(t *testing.T) {
	t.Run("runtime_parameters", func(t *testing.T) {
		arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				name, q, code, message string
				args                   []any
			}{
				{"position_rank", "SELECT array_position($1::int[],1) AS v", "0A000", "searching for elements in multidimensional arrays is not supported", []any{"{{1}}"}},
				{"positions_rank", "SELECT array_positions($1::int[],1) AS v", "0A000", "searching for elements in multidimensional arrays is not supported", []any{"{{1}}"}},
				{"remove_rank_absent", "SELECT array_remove($1::int[],9) AS v", "0A000", "removing elements from multidimensional arrays is not supported", []any{"{{1}}"}},
				{"null_start", "SELECT array_position($1::int[],$2::int,$3::int) AS v", "22004", "initial position must not be null", []any{"{NULL}", nil, nil}},
				{"rank_before_start", "SELECT array_position($1::int[],1,$2::int) AS v", "0A000", "searching for elements in multidimensional arrays is not supported", []any{"{{1}}", nil}},
				{"malformed_parameter", "SELECT array_position($1::int[],1) AS v", "22P02", "malformed array literal: \"oops\"", []any{"oops"}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					s := arraySearchRuntimeError(tc.q, tc.code, tc.message, tc.args...)
					if tc.name == "malformed_parameter" {
						s.Diagnostic.Detail = `Array value must start with "{" or dimension information.`
						s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
					}
					arraySearchBoth(t, p, b, c, s)
				})
			}
		})
	})
	t.Run("json_demand", func(t *testing.T) {
		arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, f := range []string{"array_position", "array_positions", "array_remove", "array_replace"} {
				for _, input := range []any{nil, "{}", "{NULL}", `{"{}"}`} {
					for _, search := range []any{nil, "{}"} {
						name := f + "_" + fixture.ArrayReferenceQuote(stringifyArraySearchInput(input)) + "_" + stringifyArraySearchInput(search)
						t.Run(name, func(t *testing.T) {
							q := "SELECT " + f + "($1::json[],$2::json) AS v"
							args := []any{input, search}
							if f == "array_replace" {
								q = "SELECT array_replace($1::json[],$2::json,$3::json) AS v"
								args = append(args, nil)
							}
							// Whole NULL and empty inputs return before equality lookup.
							// NULL search without NULL elements is another search-only fast path.
							success := input == nil || input == "{}" || ((f == "array_position" || f == "array_positions") && search == nil && input == `{"{}"}`)
							if !success {
								arraySearchBoth(t, p, b, c, arraySearchRuntimeError(q, "42883", "could not identify an equality operator for type json", args...))
								return
							}
							typ := "_JSON"
							var want any
							if f == "array_position" {
								typ = "INT4"
							}
							if f == "array_positions" {
								typ = "_INT4"
							}
							if input != nil && f != "array_position" {
								want = "{}"
							}
							arraySearchBoth(t, p, b, c, arraySearchSelect(b, q, []string{"v"}, []string{typ}, [][]any{{want}}, args...))
						})
					}
				}
			}
			for _, op := range []string{"@>", "<@", "&&"} {
				for _, args := range [][]any{{"{}", "{}"}, {"{NULL}", "{NULL}"}, {nil, "{}"}, {"{}", nil}} {
					q := "SELECT $1::json[] " + op + " $2::json[] AS v"
					t.Run(op+"_"+stringifyArraySearchInput(args[0])+"_"+stringifyArraySearchInput(args[1]), func(t *testing.T) {
						if args[0] == nil || args[1] == nil {
							arraySearchBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, args...))
						} else {
							arraySearchBoth(t, p, b, c, arraySearchRuntimeError(q, "42883", "could not identify an equality operator for type json", args...))
						}
					})
				}
			}
		})
	})
	t.Run("literal_phases_and_collation", func(t *testing.T) {
		arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE as_diag_empty(a INT[],s INT)", 0))
			// These immutable constants fail during execution, including over an
			// empty source. PostgreSQL Prepare does not demand their providers.
			for _, tc := range []struct{ name, q, code, message string }{
				{"position_rank", "SELECT array_position(ARRAY[[1]],1,NULL)", "0A000", "searching for elements in multidimensional arrays is not supported"},
				{"positions_rank", "SELECT array_positions(ARRAY[[1]],1)", "0A000", "searching for elements in multidimensional arrays is not supported"},
				{"remove_rank", "SELECT array_remove(ARRAY[[1]],9)", "0A000", "removing elements from multidimensional arrays is not supported"},
				{"null_start", "SELECT array_position(ARRAY[NULL]::int[],NULL,NULL)", "22004", "initial position must not be null"},
				{"json_provider", "SELECT array_position(ARRAY['{}'::json],'{}'::json)", "42883", "could not identify an equality operator for type json"},
				{"empty_source_position", "SELECT array_position(ARRAY[[1]],1) FROM as_diag_empty", "0A000", "searching for elements in multidimensional arrays is not supported"},
				{"empty_source_remove", "SELECT array_remove(ARRAY[[1]],9) FROM as_diag_empty", "0A000", "removing elements from multidimensional arrays is not supported"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					arraySearchDiagnosticBoth(t, p, b, c, tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, false)
				})
			}
			const operatorHint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			const functionHint = "No function matches the given name and argument types. You might need to add explicit type casts."
			const explicitConflict = "collation mismatch between explicit collations \"C\" and \"POSIX\""
			for _, tc := range []struct {
				name, q string
				d       fixture.Diagnostic
			}{
				{"operator_types", "SELECT ARRAY[1] @> ARRAY[1::numeric]", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: integer[] @> numeric[]", Hint: operatorHint, Position: "17"}},
				{"operator_unknown", "SELECT '{1}' @> '{1}'", fixture.Diagnostic{Code: "42725", Message: "operator is not unique: unknown @> unknown", Hint: "Could not choose a best candidate operator. You might need to add explicit type casts.", Position: "14"}},
				{"scalar_array", "SELECT array_position(1,1)", fixture.Diagnostic{Code: "42883", Message: "function array_position(integer, integer) does not exist", Hint: functionHint, Position: "8"}},
				{"position_arity", "SELECT array_position(ARRAY[1])", fixture.Diagnostic{Code: "42883", Message: "function array_position(integer[]) does not exist", Hint: functionHint, Position: "8"}},
				{"positions_arity", "SELECT array_positions(ARRAY[1],1,1)", fixture.Diagnostic{Code: "42883", Message: "function array_positions(integer[], integer, integer) does not exist", Hint: functionHint, Position: "8"}},
				{"remove_arity", "SELECT array_remove(ARRAY[1],1,1)", fixture.Diagnostic{Code: "42883", Message: "function array_remove(integer[], integer, integer) does not exist", Hint: functionHint, Position: "8"}},
				{"replace_arity", "SELECT array_replace(ARRAY[1],1)", fixture.Diagnostic{Code: "42883", Message: "function array_replace(integer[], integer) does not exist", Hint: functionHint, Position: "8"}},
				{"unicode_signature", "SELECT /*é*/array_remove(ARRAY[1],TRUE)", fixture.Diagnostic{Code: "42883", Message: "function array_remove(integer[], boolean) does not exist", Hint: functionHint, Position: "13"}},
				{"competing_types", "SELECT array_replace(ARRAY[1],TRUE,'bad')", fixture.Diagnostic{Code: "42883", Message: "function array_replace(integer[], boolean, unknown) does not exist", Hint: functionHint, Position: "8"}},
				{"bigint_start", "SELECT array_position(ARRAY[1],1,2147483648::bigint)", fixture.Diagnostic{Code: "42883", Message: "function array_position(integer[], integer, bigint) does not exist", Hint: functionHint, Position: "8"}},
				{"bad_start", "SELECT array_position(ARRAY[1],1,'bad')", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type integer: \"bad\"", Position: "34"}},
				{"distinct", "SELECT array_position(DISTINCT ARRAY[1],1)", fixture.Diagnostic{Code: "42809", Message: "DISTINCT specified, but array_position is not an aggregate function", Position: "8"}},
				{"window", "SELECT array_position(ARRAY[1],1) OVER ()", fixture.Diagnostic{Code: "42809", Message: "OVER specified, but array_position is not a window function nor an aggregate function", Position: "8"}},
				{"named_arguments", "SELECT array_position(arr => ARRAY[1], elem => 1)", fixture.Diagnostic{Code: "42883", Message: "function array_position(arr => integer[], elem => integer) does not exist", Hint: functionHint, Position: "8"}},
				{"explicit_position", "SELECT array_position(ARRAY['a'] COLLATE \"C\",'a' COLLATE \"POSIX\")", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "50"}},
				{"explicit_replacement", "SELECT array_replace(ARRAY['a'] COLLATE \"C\",'a','b' COLLATE \"POSIX\")", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "53"}},
				{"explicit_operator", "SELECT (ARRAY['a'] COLLATE \"C\") && (ARRAY['a'] COLLATE \"POSIX\")", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "48"}},
				{"explicit_constructor", "SELECT array_position(ARRAY['a' COLLATE \"C\",'b' COLLATE \"POSIX\"],'a')", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "49"}},
				{"explicit_null_position", "SELECT array_position(ARRAY[NULL::text COLLATE \"C\",NULL::text COLLATE \"POSIX\"],NULL)", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "63"}},
				{"explicit_null_remove", "SELECT array_remove(ARRAY[NULL::text COLLATE \"C\",NULL::text COLLATE \"POSIX\"],NULL)", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "61"}},
				{"explicit_null_replace", "SELECT array_replace(ARRAY[NULL::text COLLATE \"C\",NULL::text COLLATE \"POSIX\"],NULL,'a')", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "62"}},
				{"explicit_null_operator", "SELECT ARRAY[NULL::text COLLATE \"C\",NULL::text COLLATE \"POSIX\"] && ARRAY[NULL::text]", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "48"}},
				{"explicit_absent_null", "SELECT array_position(ARRAY['a' COLLATE \"C\",'b' COLLATE \"POSIX\"],NULL)", fixture.Diagnostic{Code: "42P21", Message: explicitConflict, Position: "49"}},
			} {
				t.Run(tc.name, func(t *testing.T) { arraySearchDiagnosticBoth(t, p, b, c, tc.q, tc.d, true) })
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE as_collation(a TEXT COLLATE \"C\",s TEXT COLLATE \"POSIX\",r TEXT COLLATE \"C\")", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO as_collation VALUES ('a','a','b'),(NULL,NULL,NULL)", 2))
			for _, q := range []string{
				"SELECT array_position(ARRAY[a],s) FROM as_collation",
				"SELECT array_positions(ARRAY[a],s) FROM as_collation",
				"SELECT array_remove(ARRAY[a],s) FROM as_collation",
				"SELECT array_replace(ARRAY[a],s,r) FROM as_collation",
				"SELECT ARRAY[a] && ARRAY[s] FROM as_collation",
			} {
				t.Run(q, func(t *testing.T) {
					d := fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
					arraySearchDiagnosticBoth(t, p, b, c, q, d, false)
				})
			}
			// A retained implicit conflict is not an eager failure: NULL matching
			// and empty populations never invoke string equality.
			for _, tc := range []struct {
				q, column, typ string
				rows           [][]any
			}{
				{"SELECT array_position(ARRAY[a],s) FROM as_collation WHERE a IS NULL", "array_position", "INT4", [][]any{{int64(1)}}},
				{"SELECT array_positions(ARRAY[a],s) FROM as_collation WHERE a IS NULL", "array_positions", "_INT4", [][]any{{"{1}"}}},
				{"SELECT array_remove(ARRAY[a],s) FROM as_collation WHERE a IS NULL", "array_remove", "_TEXT", [][]any{{"{}"}}},
				{"SELECT array_replace(ARRAY[a],s,r) FROM as_collation WHERE a IS NULL", "array_replace", "_TEXT", [][]any{{"{NULL}"}}},
				{"SELECT ARRAY[a] && ARRAY[s] FROM as_collation WHERE a IS NULL", "?column?", "BOOL", [][]any{{false}}},
				{"SELECT array_replace('{}'::text[],s,r) FROM as_collation", "array_replace", "_TEXT", [][]any{{"{}"}, {"{}"}}},
			} {
				t.Run(tc.q, func(t *testing.T) {
					s := arraySearchSelect(b, tc.q, []string{tc.column}, []string{tc.typ}, tc.rows)
					s.Bag = true
					arraySearchBoth(t, p, b, c, s)
				})
			}
		})
	})
}

func stringifyArraySearchInput(v any) string {
	if v == nil {
		return "NULL"
	}
	return v.(string)
}
