package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLIntegerForDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, bounds, body, code, message, expression string }{
		{"lower_null", "NULL::integer..3", "", "22004", "lower bound of FOR loop cannot be null", ""},
		{"upper_null", "1..NULL::integer", "", "22004", "upper bound of FOR loop cannot be null", ""},
		{"step_null", "1..3 BY NULL::integer", "", "22004", "BY value of FOR loop cannot be null", ""},
		{"zero", "1..3 BY 0", "", "22023", "BY value of FOR loop must be greater than zero", ""},
		{"negative", "1..3 BY -1", "", "22023", "BY value of FOR loop must be greater than zero", ""},
		{"reverse_zero", "REVERSE 3..1 BY 0", "", "22023", "BY value of FOR loop must be greater than zero", ""},
		{"reverse_negative", "REVERSE 3..1 BY -1", "", "22023", "BY value of FOR loop must be greater than zero", ""},
		{"empty_zero", "3..1 BY 0", "", "22023", "BY value of FOR loop must be greater than zero", ""},
		{"reverse_empty_negative", "REVERSE 1..3 BY -1", "", "22023", "BY value of FOR loop must be greater than zero", ""},
		{"empty_null", "3..1 BY NULL::integer", "", "22004", "BY value of FOR loop cannot be null", ""},
		{"lower_text", "'bad'::text..3", "", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{"upper_text", "1..'bad'::text", "", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{"step_text", "1..3 BY 'bad'::text", "", "22P02", `invalid input syntax for type integer: "bad"`, ""},
		{"lower_overflow", "2147483648::bigint..3", "", "22003", "integer out of range", ""},
		{"upper_overflow", "1..2147483648::bigint", "", "22003", "integer out of range", ""},
		{"step_overflow", "1..3 BY 2147483648::bigint", "", "22003", "integer out of range", ""},
		{"lower_divide", "(1/0)..3", "", "22012", "division by zero", "(1/0)"},
		{"upper_divide", "1..(1/0)", "", "22012", "division by zero", "(1/0)"},
		{"step_divide", "1..3 BY (1/0)", "", "22012", "division by zero", "(1/0)"},
		{"missing_call", "missing_integer_bound()..3", "", "42883", "function missing_integer_bound() does not exist", ""},
		{"unicode_missing", "/* é終 */ absent_bound..3", "", "42703", `column "absent_bound" does not exist`, ""},
		{"body_error", "1..3", "i:=1/0;", "22012", "division by zero", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				body := "\n-- é diagnostic coordinates\nBEGIN\n FOR i IN " + tc.bounds + " LOOP\n " + tc.body + "\n END LOOP;\n RETURN 1;\nEND"
				userOperatorExec(t, p, b, c, "CREATE FUNCTION if_error() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Context = "PL/pgSQL function if_error() line 4 at FOR with integer loop variable"
				if tc.expression != "" {
					want.Context = "PL/pgSQL expression \"" + tc.expression + "\"\n" + want.Context
				}
				if tc.name == "missing_call" {
					want.Hint = parametersNoMatchHint
					want.InternalQuery = "missing_integer_bound()"
					want.InternalPosition = "1"
				}
				if tc.name == "unicode_missing" {
					want.InternalQuery = "absent_bound"
					want.InternalPosition = "1"
				}
				if tc.name == "body_error" {
					want.Context = "PL/pgSQL assignment \"i:=1/0\"\nPL/pgSQL function if_error() line 5 at assignment"
				}
				plpgsqlError(t, p, c, "SELECT if_error() AS v", true, want)
				want.Context = strings.ReplaceAll(want.Context, "if_error()", "inline_code_block")
				plpgsqlError(t, p, c, "DO $$"+strings.Replace(body, " RETURN 1;", "", 1)+"$$", false, want)
				plpgsqlValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
			})
		})
	}
	// Exact syntax/target/alias admission proposals. Positions are character
	// coordinates in these specimens; Stage B must resolve PostgreSQL's scanner
	// phase, which can select an earlier token than the eventual missing clause.
	for _, tc := range []struct{ name, declaration, statement, code, message, token string }{
		{"missing_lower", "", "FOR i IN ..3 LOOP END LOOP;", "42601", "missing expression at or near \"..\"", ".."},
		{"missing_range", "", "FOR i IN 1 LOOP END LOOP;", "42601", `syntax error at or near "1"`, "1 LOOP"},
		{"missing_upper", "", "FOR i IN 1.. LOOP END LOOP;", "42601", "missing expression at or near \"LOOP\"", "LOOP END"},
		{"missing_step", "", "FOR i IN 1..3 BY LOOP END LOOP;", "42601", "missing expression at or near \"LOOP\"", "LOOP END"},
		{"missing_loop", "", "FOR i IN 1..3 END LOOP;", "42601", `syntax error at or near ";"`, ";"},
		{"missing_end_loop", "", "FOR i IN 1..3 LOOP", "42601", "syntax error at end of input", ""},
		{"missing_semicolon", "", "FOR i IN 1..3 LOOP END LOOP RETURN 1;", "42601", `syntax error at or near "1"`, "1;"},
		{"trailing", "", "FOR i IN 1..3 LOOP END LOOP; nonsense", "42601", "unexpected end of function definition at end of input", ""},
		{"unreachable_malformed", "", "IF false THEN FOR i IN 1.. LOOP END LOOP; END IF;", "42601", "missing expression at or near \"LOOP\"", "LOOP END"},
		{"list", "a integer; b integer;", "FOR a,b IN 1..3 LOOP END LOOP;", "42601", "integer FOR loop must have only one target variable", "a,b"},
		{"subscript", "a integer[];", "FOR a[1] IN 1..3 LOOP END LOOP;", "42601", `syntax error at or near "["`, "[1]"},
		{"reverse_query", "r record;", "FOR r IN REVERSE SELECT 1 LOOP END LOOP;", "42601", "cannot specify REVERSE in query FOR loop", "REVERSE"},
		{"mismatch_unicode", "", `<<"é">> FOR i IN 1..3 LOOP END LOOP "終";`, "42601", `end label "終" differs from block's label "é"`, `"終"`},
		{"unlabeled_end", "", "FOR i IN 1..3 LOOP END LOOP x;", "42601", "end label \"x\" specified for unlabeled block", "x;"},
		{"quoted_backslash", "", `<<"a\b">> FOR i IN 1..3 LOOP END LOOP "c\d";`, "42601", `end label "c\d" differs from block's label "a\b"`, `"c\d"`},
		{"embedded_quote", "", `<<"a""b">> FOR i IN 1..3 LOOP END LOOP "c""d";`, "42601", `end label "c"d" differs from block's label "a"b"`, `"c""d"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				declaration := ""
				if tc.declaration != "" {
					declaration = "DECLARE " + tc.declaration + " "
				}
				body := "\n-- é admission\n" + declaration + "BEGIN " + tc.statement + " END"
				for _, prefix := range []string{"CREATE FUNCTION if_bad() RETURNS void LANGUAGE plpgsql AS $$", "DO $$"} {
					q := prefix + body + "$$"
					want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if tc.token != "" {
						at := strings.Index(q, tc.token)
						want.Position = fixture.Position(utf8.RuneCountInString(q[:at]) + 1)
					} else {
						want.Position = fixture.Position(utf8.RuneCountInString(q) - 1)
					}
					plpgsqlError(t, p, c, q, false, want)
				}
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='if_bad'", "INT8", int64(0))
				userOperatorExec(t, p, b, c, "CREATE FUNCTION if_bad() RETURNS integer LANGUAGE plpgsql AS $$BEGIN FOR i IN 1..1 LOOP RETURN i; END LOOP; RETURN 0; END$$")
				plpgsqlValue(t, p, b, c, "SELECT if_bad() AS v", "INT4", int64(1))
			})
		})
	}
	for _, bounds := range []string{"1 AS lo..3 AS hi BY 1 AS stride", "(1)..(3) BY (1)", "abs(-1)..abs(-3)", "1..3 BY 1"} {
		t.Run("scalar_admission_"+bounds, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION if_alias() RETURNS integer LANGUAGE plpgsql AS $$DECLARE s integer:=0; BEGIN <<ok>> FOR i IN "+bounds+" LOOP s:=s+i; END LOOP ok; RETURN s; END$$")
				plpgsqlValue(t, p, b, c, "SELECT if_alias() AS v", "INT4", int64(6))
			})
		})
	}
	t.Run("qualified", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			integerForTypeWitness(t, p, b, c)
			// NameOfDatum retains the dotted spelling as ONE new variable name.
			// The ordinary unquoted field expression still addresses the record.
			userOperatorExec(t, p, b, c, `CREATE FUNCTION if_qualified() RETURNS text LANGUAGE plpgsql AS $$DECLARE r record; "r.f1" text:='outer'; s text:=''; BEGIN r:=ROW(7); <<n>> FOR r.f1 IN r.f1..r.f1+2 LOOP s:=s||n."r.f1"::text||':'||if_type_name("r.f1")||':'||r.f1::text||','; END LOOP n; RETURN s||"r.f1"||':'||r.f1::text; END$$`)
			plpgsqlValue(t, p, b, c, "SELECT if_qualified() AS v", "TEXT", "7:integer:7,8:integer:7,9:integer:7,outer:7")
			s := fixture.Exec("DO $$DECLARE r record; BEGIN FOR r.f1 IN 1..3 LOOP END LOOP; END$$", 0)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		})
	})
	t.Run("parameter", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			integerForTypeWitness(t, p, b, c)
			// The positional-looking target is a new name, not a formal write.
			// Bounds see the input, body sees the private INT4, postloop sees input.
			userOperatorExec(t, p, b, c, `CREATE FUNCTION if_parameter(original integer) RETURNS text LANGUAGE plpgsql AS $$DECLARE s text:=''; BEGIN <<n>> FOR $1 IN $1..$1+2 LOOP s:=s||$1::text||':'||if_type_name(n."$1")||':'||original::text||','; END LOOP n; RETURN s||$1::text; END$$`)
			value := fixture.Select("SELECT if_parameter($1) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"7:integer:7,8:integer:7,9:integer:7,7"}}, 7)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, value)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(value))
			userOperatorExec(t, p, b, c, `CREATE FUNCTION if_parameter_zero() RETURNS text LANGUAGE plpgsql AS $$DECLARE s text:=''; BEGIN FOR $1 IN 1..3 LOOP s:=s||$1::text; END LOOP; RETURN s; END$$`)
			plpgsqlValue(t, p, b, c, "SELECT if_parameter_zero() AS v", "TEXT", "123")
			s := fixture.Exec("DO $$BEGIN FOR $1 IN 1..3 LOOP END LOOP; END$$", 0)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		})
	})
}
