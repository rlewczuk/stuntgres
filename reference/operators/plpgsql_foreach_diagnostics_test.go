package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLForeachDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, slice, source, body, code, message string
	}{
		{"null_scalar", "x integer", " SLICE 2", "NULL::integer", "", "22004", "FOREACH expression must not be null"},
		{"null_array", "x integer", "", "NULL::integer[]", "", "22004", "FOREACH expression must not be null"},
		{"nonarray", "x integer", "", "1", "", "42804", "FOREACH expression must yield an array, not type integer"},
		{"empty_array_target", "x integer[]", "", "'{}'::integer[]", "", "42804", "FOREACH loop variable must not be of an array type"},
		{"populated_array_target", "x integer[]", " SLICE 0", "'{1}'::integer[]", "", "42804", "FOREACH loop variable must not be of an array type"},
		{"empty_slice", "x integer", " SLICE 1", "'{}'::integer[]", "", "2202E", "slice dimension (1) is out of the valid range 0..0"},
		{"rank_precedes_target", "x integer", " SLICE 2", "'{1}'::integer[]", "", "2202E", "slice dimension (2) is out of the valid range 0..1"},
		{"scalar_slice_target", "x integer", " SLICE 1", "'{1}'::integer[]", "", "42804", "FOREACH ... SLICE loop variable must be of an array type"},
		{"item_conversion", "x integer", "", "'{1,bad}'::text[]", "", "22P02", `invalid input syntax for type integer: "bad"`},
		{"not_null_item", "x integer NOT NULL :=99", "", "'{1,NULL}'::integer[]", "", "22004", `null value cannot be assigned to variable "x" declared NOT NULL`},
		{"source_arithmetic", "x integer", "", "1/0", "", "22012", "division by zero"},
		{"body_arithmetic", "x integer", "", "'{1}'::integer[]", "x:=1/0;", "22012", "division by zero"},
		{"utf8_missing_source", "x integer", "", "/* é */ missing_array", "", "42703", `column "missing_array" does not exist`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				body := "\n-- é diagnostic coordinates\nDECLARE " + tc.declaration + ";\nBEGIN\n  FOREACH x" + tc.slice + " IN ARRAY " + tc.source + " LOOP\n    " + tc.body + "\n  END LOOP;\n  RETURN 1;\nEND "
				userOperatorExec(t, p, b, c, "CREATE FUNCTION fe_error() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Context = "PL/pgSQL function fe_error() line 5 at FOREACH over array"
				switch tc.name {
				case "source_arithmetic":
					want.Context = "PL/pgSQL expression \"1/0\"\n" + want.Context
				case "body_arithmetic":
					want.Context = "PL/pgSQL assignment \"x:=1/0\"\nPL/pgSQL function fe_error() line 6 at assignment"
				case "utf8_missing_source":
					want.InternalQuery, want.InternalPosition = "missing_array", "1"
				}
				plpgsqlError(t, p, c, "SELECT fe_error() AS v", true, want)
				plpgsqlValue(t, p, b, c, "SELECT 7 AS v", "INT4", int64(7))
				if tc.name == "null_array" {
					want.Context = "PL/pgSQL function inline_code_block line 5 at FOREACH over array"
					plpgsqlError(t, p, c, "DO $$\n-- é DO sibling\nDECLARE x integer;\nBEGIN\n FOREACH x IN ARRAY NULL::integer[] LOOP END LOOP;\nEND$$", false, want)
				}
			})
		})
	}
	for _, tc := range []struct {
		name, declaration, statement, code, message, position string
	}{
		{"negative", "x integer;", "FOREACH x SLICE -1 IN ARRAY '{1}'::integer[] LOOP END LOOP;", "42601", `syntax error at or near "-"`, "121"},
		{"decimal", "x integer;", "FOREACH x SLICE 1.0 IN ARRAY '{1}'::integer[] LOOP END LOOP;", "42601", `syntax error at or near "1.0"`, "121"},
		{"parameter", "x integer;", "FOREACH x SLICE $1 IN ARRAY '{1}'::integer[] LOOP END LOOP;", "42601", `syntax error at or near "$1"`, "121"},
		{"overflow", "x integer;", "FOREACH x SLICE 2147483648 IN ARRAY '{1}'::integer[] LOOP END LOOP;", "42601", `syntax error at or near "2147483648"`, "121"},
		{"slice_missing_integer", "x integer;", "FOREACH x SLICE IN ARRAY '{1}'::integer[] LOOP END LOOP;", "42601", `syntax error at or near "IN"`, "121"},
		{"duplicate_slice", "x integer;", "FOREACH x SLICE 1 SLICE 1 IN ARRAY '{1}'::integer[] LOOP END LOOP;", "42601", `syntax error at or near "SLICE"`, "123"},
		{"missing_array", "x integer;", "FOREACH x IN '{1}'::integer[] LOOP END LOOP;", "42601", `syntax error at or near "'{1}'"`, "118"},
		{"missing_loop", "x integer;", "FOREACH x IN ARRAY '{1}'::integer[] END LOOP;", "42601", `syntax error at or near ";"`, "149"},
		{"missing_end_loop", "x integer;", "FOREACH x IN ARRAY '{1}'::integer[] LOOP x:=x;", "42601", "syntax error at end of input", "165"},
		{"trailing_garbage", "x integer;", "FOREACH x IN ARRAY '{1}'::integer[] LOOP END LOOP; nonsense", "42601", `syntax error at or near "nonsense"`, "156"},
		{"unknown_unreachable", "", "IF false THEN FOREACH absent IN ARRAY '{1}'::integer[] LOOP END LOOP; END IF;", "42601", "loop variable of FOREACH must be a known variable or list of variables", "117"},
		{"constant_unreachable", "x CONSTANT integer:=1;", "IF false THEN FOREACH x IN ARRAY '{1}'::integer[] LOOP END LOOP; END IF;", "22005", `variable "x" is declared CONSTANT`, "139"},
		{"mismatched_unicode_label", "x integer;", `<<"é">> FOREACH x IN ARRAY '{1}'::integer[] LOOP END LOOP "終";`, "42601", `end label "終" differs from block's label "é"`, "163"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				q := "CREATE FUNCTION fe_bad() RETURNS integer LANGUAGE plpgsql AS $$\n-- é admission\nDECLARE " + tc.declaration + "\nBEGIN\n" + tc.statement + "\nRETURN 0; END$$"
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = tc.position
				plpgsqlError(t, p, c, q, false, want)
				plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='fe_bad'", "INT8", int64(0))
				userOperatorExec(t, p, b, c, "CREATE FUNCTION fe_bad() RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer; BEGIN FOREACH x IN ARRAY '{12}'::integer[] LOOP END LOOP; RETURN x; END$$")
				plpgsqlValue(t, p, b, c, "SELECT fe_bad() AS v", "INT4", int64(12))
			})
		})
	}
	t.Run("held_recovery", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION fe_convert(a text[]) RETURNS integer LANGUAGE plpgsql AS $$DECLARE x integer:=99; BEGIN FOREACH x IN ARRAY a LOOP END LOOP; RETURN x; END$$")
			q := "SELECT fe_convert($1::text[]) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}, "{12}"))
			for _, input := range []any{"{1,bad}", nil} {
				want := plpgsqlRuntimeDiagnostic("22004", "FOREACH expression must not be null", "fe_convert(text[])", "FOREACH over array")
				if input != nil {
					want = plpgsqlRuntimeDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, "fe_convert(text[])", "FOREACH over array")
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, want, input)
					s.SelectRows, s.ErrorStage, s.Prepared = true, "execute", prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				s := fixture.ErrorDiagnostic(q, want, input)
				s.SelectRows, s.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(34)}}, "{34}"))
			}
		})
	})
}
