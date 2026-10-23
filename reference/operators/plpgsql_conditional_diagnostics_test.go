package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Complete diagnostics include every optional field, including absence.
func conditionalDiagnostic(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, query bool, want fixture.Diagnostic) {
	t.Helper()
	fixture.EnumReferenceErrorChecked(t, p, c, q, query, "execute", func(t *testing.T, got fixture.Diagnostic) {
		if got != want {
			t.Errorf("conditional diagnostic query=%q got=%#v want=%#v", q, got, want)
		}
	})
}

func conditionalWant(code, message, action string) fixture.Diagnostic {
	want := plpgsqlRuntimeDiagnostic(code, message, "conditional_error()", action)
	if code == "20000" {
		want.Hint = "CASE statement is missing ELSE part."
	}
	return want
}

func conditionalRuntimeCase(t *testing.T, body, code, message, action string) {
	t.Helper()
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_error() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
		conditionalDiagnostic(t, p, c, "SELECT conditional_error() AS v", true, conditionalWant(code, message, action))
	})
}

func TestReferencePLpgSQLConditionalConversion(t *testing.T) {
	for _, form := range []string{"if", "case"} {
		for _, tc := range []struct {
			name, expression string
			value            int64
		}{
			{"yes", "'yes'::text", 11},
			{"off", "'off'::text", 22},
			{"null_text", "NULL::text", 22},
			{"numeric_true", "1", 11},
			{"numeric_false", "0", 22},
		} {
			t.Run(form+"_"+tc.name, func(t *testing.T) {
				body := "BEGIN IF " + tc.expression + " THEN RETURN 11; ELSE RETURN 22; END IF; END"
				if form == "case" {
					body = "BEGIN CASE WHEN " + tc.expression + " THEN RETURN 11; ELSE RETURN 22; END CASE; END"
				}
				conditionalBody(t, body, tc.value)
			})
		}
	}
	t.Run("invalid_if", func(t *testing.T) {
		conditionalRuntimeCase(t, "BEGIN IF 'nope'::text THEN RETURN 11; END IF; RETURN 22; END", "22P02", `invalid input syntax for type boolean: "nope"`, "IF")
	})
	t.Run("invalid_case", func(t *testing.T) {
		conditionalRuntimeCase(t, "BEGIN CASE WHEN 'nope'::text THEN RETURN 11; ELSE RETURN 22; END CASE; END", "22P02", `invalid input syntax for type boolean: "nope"`, "CASE")
	})
	t.Run("skipped_invalid", func(t *testing.T) {
		conditionalBody(t, "BEGIN IF true THEN CASE WHEN true THEN RETURN 11; WHEN 'nope'::text THEN RETURN 99; END CASE; ELSIF 'nope'::text THEN RETURN 98; END IF; END", 11)
	})
	for _, form := range []string{"if", "case"} {
		t.Run(form+"_selected_cast", func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Enum text 'yes' has successful bool IO. The selected cast's
				// division failure must propagate, never retry that IO conversion.
				userOperatorExec(t, p, b, c,
					"CREATE TYPE conditional_bool_source AS ENUM ('yes')",
					"CREATE FUNCTION conditional_bool_fail(conditional_bool_source) RETURNS boolean LANGUAGE SQL AS 'SELECT 1/0=0'",
					"CREATE CAST (conditional_bool_source AS boolean) WITH FUNCTION conditional_bool_fail(conditional_bool_source) AS ASSIGNMENT")
				body := "BEGIN IF 'yes'::conditional_bool_source THEN RETURN 11; ELSE RETURN 22; END IF; END"
				action := "IF"
				if form == "case" {
					body = "BEGIN CASE WHEN 'yes'::conditional_bool_source THEN RETURN 11; ELSE RETURN 22; END CASE; END"
					action = "CASE"
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_error() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
				want := conditionalWant("22012", "division by zero", action)
				want.Context = "SQL function \"conditional_bool_fail\" statement 1\n" + want.Context
				conditionalDiagnostic(t, p, c, "SELECT conditional_error() AS v", true, want)
				plpgsqlValue(t, p, b, c, "SELECT 'yes'::conditional_bool_source::text AS v", "TEXT", "yes")
			})
		})
	}
}

func TestReferencePLpgSQLConditionalDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, body, code, message, action string }{
		{"if_division", "BEGIN IF 1/0=0 THEN RETURN 1; END IF; RETURN 2; END", "22012", "division by zero", "IF"},
		{"elsif_division", "BEGIN IF false THEN RETURN 1; ELSIF 1/0=0 THEN RETURN 2; END IF; RETURN 3; END", "22012", "division by zero", "IF"},
		{"searched_division", "BEGIN CASE WHEN 1/0=0 THEN RETURN 1; ELSE RETURN 2; END CASE; END", "22012", "division by zero", "CASE"},
		{"search_division", "BEGIN CASE 1/0 WHEN 1 THEN RETURN 1; ELSE RETURN 2; END CASE; END", "22012", "division by zero", "CASE"},
		{"when_division", "BEGIN CASE 2 WHEN 1/0 THEN RETURN 1; ELSE RETURN 2; END CASE; END", "22012", "division by zero", "CASE"},
		{"return_division", "BEGIN IF true THEN CASE WHEN true THEN RETURN 1/0; END CASE; END IF; RETURN 2; END", "22012", "division by zero", "RETURN"},
		{"assignment_division", "DECLARE x integer; BEGIN IF true THEN x := 1/0; END IF; RETURN x; END", "22012", "division by zero", "assignment"},
		{"when_type", "BEGIN CASE 2 WHEN true THEN RETURN 1; ELSE RETURN 2; END CASE; END", "42883", "operator does not exist: integer = boolean", "CASE"},
		{"unicode_missing", "DECLARE s text := 'é'; BEGIN IF conditional_missing() THEN RETURN 1; END IF; RETURN 2; END", "42883", "function conditional_missing() does not exist", "IF"},
		{"multiline", "BEGIN\n IF false THEN RETURN 1;\n ELSIF 'nope'::text THEN RETURN 2;\n END IF;\n RETURN 3;\nEND", "22P02", `invalid input syntax for type boolean: "nope"`, "IF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_error() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				want := conditionalWant(tc.code, tc.message, tc.action)
				switch tc.name {
				case "if_division", "elsif_division", "searched_division":
					want.Context = "PL/pgSQL expression \"1/0=0\"\n" + want.Context
				case "search_division", "return_division":
					want.Context = "PL/pgSQL expression \"1/0\"\n" + want.Context
				case "when_division":
					want.Context = "PL/pgSQL expression \"\"__Case__Variable_1__\" IN (1/0)\"\n" + want.Context
				case "assignment_division":
					want.Context = "PL/pgSQL assignment \"x := 1/0\"\n" + want.Context
				case "when_type":
					want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
					want.InternalPosition = "24"
					want.InternalQuery = `"__Case__Variable_1__" IN (true)`
				case "unicode_missing":
					want.Hint = parametersNoMatchHint
					want.InternalPosition = "1"
					want.InternalQuery = "conditional_missing()"
				case "multiline":
					want.Context = "PL/pgSQL function conditional_error() line 2 at IF"
				}
				conditionalDiagnostic(t, p, c, "SELECT conditional_error() AS v", true, want)
			})
		})
	}
	for _, tc := range []struct{ name, body, code, message, createPosition, doPosition string }{
		{"if_missing_expression", "BEGIN IF THEN RETURN; END IF; END", "42601", `missing expression at or near "THEN"`, "82", "15"},
		{"when_missing_expression", "BEGIN CASE WHEN THEN RETURN; END CASE; END", "42601", `missing expression at or near "THEN"`, "89", "22"},
		{"simple_when_missing_expression", "BEGIN CASE 1 WHEN THEN RETURN; END CASE; END", "42601", `missing expression at or near "THEN"`, "91", "24"},
		{"missing_then", "BEGIN IF true; RETURN; END IF; END", "42601", `missing "THEN" at end of SQL expression`, "86", "19"},
		{"missing_if_close", "BEGIN IF true THEN RETURN; END", "42601", `syntax error at end of input`, "103", "36"},
		{"missing_case_close", "BEGIN CASE WHEN true THEN RETURN; END", "42601", `syntax error at end of input`, "110", "43"},
		{"mismatched_if", "BEGIN IF true THEN RETURN; END CASE; END", "42601", `syntax error at or near "CASE"`, "104", "37"},
		{"mismatched_case", "BEGIN CASE WHEN true THEN RETURN; END IF; END", "42601", `syntax error at or near "IF"`, "111", "44"},
		// RETURN legality is checked before its malformed scalar expression.
		{"unreachable_void_return", "BEGIN IF false THEN RETURN 1+; END IF; RETURN; END", "42804", "RETURN cannot have a parameter in function returning void", "100", "33"},
		{"void_return_suffix", "BEGIN IF true THEN RETURN; END IF; RETURN 1+; END", "42804", "RETURN cannot have a parameter in function returning void", "115", "48"},
		// Assignment avoids the void-RETURN precedence and witnesses full parsing.
		{"unreachable_malformed", "DECLARE x integer; BEGIN IF false THEN x := 1+; END IF; RETURN; END", "42601", `syntax error at end of input`, "119", "52"},
		{"malformed_suffix", "DECLARE x integer; BEGIN IF true THEN RETURN; END IF; x := 1+; END", "42601", `syntax error at end of input`, "134", "67"},
	} {
		for _, entry := range []string{"create", "do"} {
			t.Run(tc.name+"_"+entry, func(t *testing.T) {
				plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := "DO $$" + tc.body + "$$"
					if entry == "create" {
						q = "CREATE FUNCTION conditional_syntax() RETURNS void LANGUAGE plpgsql AS $$" + tc.body + "$$"
					}
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					want.Position = tc.doPosition
					if entry == "create" {
						want.Position = tc.createPosition
					}
					conditionalDiagnostic(t, p, c, q, false, want)
				})
			})
		}
	}
	t.Run("held_reached_binding", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_lazy(x boolean) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF x THEN RETURN conditional_missing(); ELSE RETURN 7; END IF; END$$")
			q := "SELECT conditional_lazy($1) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, false))
			rows, err := stmt.QueryContext(p.Ctx, true)
			if rows != nil {
				rows.Close()
				t.Fatal("failed held invocation exposed rows")
			}
			if err == nil {
				t.Fatal("reached missing call succeeded")
			}
			want := plpgsqlRuntimeDiagnostic("42883", "function conditional_missing() does not exist", "conditional_lazy(boolean)", "RETURN")
			want.Hint = parametersNoMatchHint
			want.InternalPosition = "1"
			want.InternalQuery = "conditional_missing()"
			got := fixture.ExtractDiagnostic(t, err)
			if got != want {
				t.Errorf("held diagnostic got=%#v want=%#v", got, want)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}, false))
		})
	})
}
