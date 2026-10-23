package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLConditionalReviewProvenance(t *testing.T) {
	// PostgreSQL allocates diagnostic CASE datums after all arm bodies have
	// been parsed. Private invocation slots need not share that allocation order.
	for _, tc := range []struct{ name, body, diagnosticQuery, position string }{
		{
			"datum_nested_case",
			"BEGIN CASE 1 WHEN true THEN CASE 2 WHEN 2 THEN RETURN 7; ELSE RETURN 8; END CASE; ELSE RETURN 9; END CASE; END",
			`"__Case__Variable_2__" IN (true)`, "24",
		},
		{
			"datum_nested_declaration",
			"BEGIN CASE 1 WHEN true THEN DECLARE x integer; BEGIN RETURN 7; END; ELSE RETURN 9; END CASE; END",
			`"__Case__Variable_2__" IN (true)`, "24",
		},
		{
			"datum_mixed_nested",
			"BEGIN CASE 1 WHEN true THEN DECLARE x integer; y integer; BEGIN CASE 2 WHEN 2 THEN RETURN 7; ELSE RETURN 8; END CASE; END; ELSE RETURN 9; END CASE; END",
			`"__Case__Variable_4__" IN (true)`, "24",
		},
		{
			"datum_two_digit_width",
			"BEGIN CASE 1 WHEN true THEN DECLARE a1 integer; a2 integer; a3 integer; a4 integer; a5 integer; a6 integer; a7 integer; a8 integer; a9 integer; BEGIN RETURN 7; END; ELSE RETURN 9; END CASE; END",
			`"__Case__Variable_10__" IN (true)`, "25",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_review_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				want := plpgsqlRuntimeDiagnostic("42883", "operator does not exist: integer = boolean", "conditional_review_error()", "CASE")
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				want.InternalQuery, want.InternalPosition = tc.diagnosticQuery, tc.position
				conditionalDiagnostic(t, p, c, "SELECT conditional_review_error() AS v", true, want)
			})
		})
	}

	// THEN/WHEN terminate at parenthesis depth zero, including tokens inside a
	// later bare SQL CASE. CREATE and DO must reject the complete body alike.
	for _, tc := range []struct{ name, body, createPosition, doPosition string }{
		{"delimiter_prefixed_if", "BEGIN IF NOT CASE WHEN true THEN false ELSE true END THEN RETURN; ELSE RETURN; END IF; END", "107", "33"},
		{"delimiter_later_if", "BEGIN IF true AND CASE WHEN true THEN true ELSE false END THEN RETURN; END IF; END", "112", "38"},
		{"delimiter_prefixed_searched_when", "BEGIN CASE WHEN NOT CASE WHEN true THEN false ELSE true END THEN RETURN; ELSE RETURN; END CASE; END", "114", "40"},
		{"delimiter_later_when_item", "BEGIN CASE 2 WHEN 1, CASE WHEN true THEN 2 ELSE 3 END THEN RETURN; ELSE RETURN; END CASE; END", "115", "41"},
		{"delimiter_prefixed_search", "BEGIN CASE 1+CASE WHEN true THEN 1 ELSE 2 END WHEN 2 THEN RETURN; ELSE RETURN; END CASE; END", "97", "23"},
	} {
		for _, entry := range []string{"create", "do"} {
			t.Run(tc.name+"_"+entry, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := "DO $$" + tc.body + "$$"
					if entry == "create" {
						q = "CREATE FUNCTION conditional_review_syntax() RETURNS void LANGUAGE plpgsql AS $$" + tc.body + "$$"
					}
					want := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
					want.Position = tc.doPosition
					if entry == "create" {
						want.Position = tc.createPosition
					}
					conditionalDiagnostic(t, p, c, q, false, want)
				})
			})
		}
	}
	for _, tc := range []struct{ name, body string }{
		{"parenthesized_prefixed_if", "BEGIN IF NOT (CASE WHEN true THEN false ELSE true END) THEN RETURN 7; ELSE RETURN 8; END IF; END"},
		{"parenthesized_later_if", "BEGIN IF true AND (CASE WHEN true THEN true ELSE false END) THEN RETURN 7; ELSE RETURN 8; END IF; END"},
		{"parenthesized_searched_when", "BEGIN CASE WHEN NOT (CASE WHEN true THEN false ELSE true END) THEN RETURN 7; ELSE RETURN 8; END CASE; END"},
		{"parenthesized_later_when_item", "BEGIN CASE 2 WHEN 1, (CASE WHEN true THEN 2 ELSE 3 END) THEN RETURN 7; ELSE RETURN 8; END CASE; END"},
		{"parenthesized_search", "BEGIN CASE 1+(CASE WHEN true THEN 1 ELSE 2 END) WHEN 2 THEN RETURN 7; ELSE RETURN 8; END CASE; END"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_review_value() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				parametersValue(t, p, b, c, "SELECT conditional_review_value() AS v", "v", "INT4", int64(7))
			})
		})
	}

	// Trailing trivia is outside the diagnostic query; genuine interior comments
	// remain. These expectations come from the released reader, not native output.
	for _, tc := range []struct{ name, body, action, diagnosticQuery, position string }{
		{
			"trailing_if_block",
			"BEGIN IF conditional_missing() /* trailing provenance comment */ THEN RETURN 1; END IF; RETURN 2; END",
			"IF", "conditional_missing()", "1",
		},
		{
			"trailing_if_line",
			"BEGIN IF conditional_missing() -- trailing provenance comment\nTHEN RETURN 1; END IF; RETURN 2; END",
			"IF", "conditional_missing()", "1",
		},
		{
			"trailing_when_block",
			"BEGIN CASE 1 WHEN conditional_missing() /* trailing provenance comment */ THEN RETURN 1; ELSE RETURN 2; END CASE; END",
			"CASE", `"__Case__Variable_1__" IN (conditional_missing())`, "28",
		},
		{
			"trailing_when_line",
			"BEGIN CASE 1 WHEN conditional_missing() -- trailing provenance comment\nTHEN RETURN 1; ELSE RETURN 2; END CASE; END",
			"CASE", `"__Case__Variable_1__" IN (conditional_missing())`, "28",
		},
		{
			"interior_if_comment",
			"BEGIN IF conditional_missing(/* interior provenance comment */) /* trailing provenance comment */ THEN RETURN 1; END IF; RETURN 2; END",
			"IF", "conditional_missing(/* interior provenance comment */)", "1",
		},
		{
			"interior_later_when_item",
			"BEGIN CASE 1 WHEN 0, conditional_missing(/* interior provenance comment */) /* trailing provenance comment */ THEN RETURN 1; ELSE RETURN 2; END CASE; END",
			"CASE", `"__Case__Variable_1__" IN (0, conditional_missing(/* interior provenance comment */))`, "31",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_review_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				want := plpgsqlRuntimeDiagnostic("42883", "function conditional_missing() does not exist", "conditional_review_error()", tc.action)
				want.Hint = parametersNoMatchHint
				want.InternalQuery, want.InternalPosition = tc.diagnosticQuery, tc.position
				conditionalDiagnostic(t, p, c, "SELECT conditional_review_error() AS v", true, want)
			})
		})
	}
	// RECFIELD datums are syntactic, deduplicated by parent record and first
	// field, and do not require the field to exist or its branch to be reached.
	for _, tc := range []struct{ name, body, diagnosticQuery string }{
		{"record_first_field", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.a; ELSE RETURN 9; END CASE; END", `"__Case__Variable_3__" IN (true)`},
		{"record_repeated_field", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.a+r.a; ELSE RETURN 9; END CASE; END", `"__Case__Variable_3__" IN (true)`},
		{"record_distinct_fields", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.a+r.b; ELSE RETURN 9; END CASE; END", `"__Case__Variable_4__" IN (true)`},
		{"record_alias_identity", "DECLARE r record; a ALIAS FOR r; BEGIN CASE 1 WHEN true THEN RETURN a.f1+r.f1; ELSE RETURN 9; END CASE; END", `"__Case__Variable_3__" IN (true)`},
		{"record_qualified_identity", "<<scope>> DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN scope.r.f1+r.f1; ELSE RETURN 9; END CASE; END scope", `"__Case__Variable_3__" IN (true)`},
		{"record_unreachable_missing_field", "DECLARE r record; BEGIN CASE 1 WHEN true THEN IF false THEN RETURN r.missing_field; END IF; RETURN 7; ELSE RETURN 9; END CASE; END", `"__Case__Variable_3__" IN (true)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_review_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				want := plpgsqlRuntimeDiagnostic("42883", "operator does not exist: integer = boolean", "conditional_review_error()", "CASE")
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				want.InternalQuery, want.InternalPosition = tc.diagnosticQuery, "24"
				conditionalDiagnostic(t, p, c, "SELECT conditional_review_error() AS v", true, want)
			})
		})
	}
	// Compound-name recognition allocates datums before SQL decides whether
	// the occurrence is a column, callee or another qualified-name context.
	for _, tc := range []struct{ name, body, diagnosticQuery, position string }{
		{"callee_record_field", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.absent(); ELSE RETURN 9; END CASE; END", `"__Case__Variable_3__" IN (true)`, "24"},
		{"callee_column_same_identity", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.absent()+r.absent; ELSE RETURN 9; END CASE; END", `"__Case__Variable_3__" IN (true)`, "24"},
		{"callee_decimal_width", "DECLARE r record; a1 integer; a2 integer; a3 integer; a4 integer; a5 integer; a6 integer; a7 integer; BEGIN CASE 1 WHEN true THEN RETURN r.absent(); ELSE RETURN 9; END CASE; END", `"__Case__Variable_10__" IN (true)`, "25"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_review_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				want := plpgsqlRuntimeDiagnostic("42883", "operator does not exist: integer = boolean", "conditional_review_error()", "CASE")
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				want.InternalQuery, want.InternalPosition = tc.diagnosticQuery, tc.position
				conditionalDiagnostic(t, p, c, "SELECT conditional_review_error() AS v", true, want)
			})
		})
	}
	// Compound-name lookup excludes scalar shadows and prefers an eligible
	// unqualified record before a competing outer block-label qualification.
	for _, tc := range []struct{ name, body string }{
		{"namespace_scalar_shadow", "DECLARE r record; BEGIN DECLARE r integer; BEGIN CASE 1 WHEN true THEN RETURN r.absent(); ELSE RETURN 9; END CASE; END; END"},
		{"namespace_record_before_label", "<<s>> DECLARE r record; BEGIN DECLARE s record; BEGIN CASE 1 WHEN true THEN RETURN s.r.f+s.r; ELSE RETURN 9; END CASE; END; END s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_review_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				want := plpgsqlRuntimeDiagnostic("42883", "operator does not exist: integer = boolean", "conditional_review_error()", "CASE")
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
				want.InternalQuery, want.InternalPosition = `"__Case__Variable_4__" IN (true)`, "24"
				conditionalDiagnostic(t, p, c, "SELECT conditional_review_error() AS v", true, want)
			})
		})
	}
	t.Run("runtime_when_execution", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_runtime_when(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN CASE 1 WHEN 12/x THEN RETURN 7; ELSE RETURN 8; END CASE; END$$")
			want := plpgsqlRuntimeDiagnostic("22012", "division by zero", "conditional_runtime_when(integer)", "CASE")
			conditionalDiagnostic(t, p, c, "SELECT conditional_runtime_when(0) AS v", true, want)
		})
	})
}
