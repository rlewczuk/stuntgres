package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Complete diagnostics, including definition positions, are pinned18.6 oracle expectations.
func TestReferencePLpgSQLReturnNextDiagnostics(t *testing.T) {
	t.Run("row_shape", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TYPE rn_expected AS(id integer)", "CREATE TYPE rn_short AS(label varchar(3))")
			for _, tc := range []struct{ name, body, detail string }{
				{"rn_width", "SELECT 1,2 INTO r;", "Number of returned columns (2) does not match expected column count (1)."},
				{"rn_type", "SELECT 1::bigint INTO r;", "Returned type bigint does not match expected type integer in column \"id\" (position 1)."},
				{"rn_precedence", "SELECT 1::bigint,2 INTO r;", "Returned type bigint does not match expected type integer in column \"id\" (position 1)."},
				{"rn_typmod", "SELECT 'a'::varchar(4) INTO r;", "Returned type character varying(4) does not match expected type character varying(3) in column \"label\" (position 1)."},
			} {
				result := "rn_expected"
				if tc.name == "rn_typmod" {
					result = "rn_short"
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION "+tc.name+"() RETURNS SETOF "+result+" LANGUAGE plpgsql AS $$DECLARE r record; BEGIN "+tc.body+" RETURN NEXT r; END$$")
				d := plpgsqlRuntimeDiagnostic("42804", "wrong record type supplied in RETURN NEXT", tc.name+"()", "RETURN NEXT")
				d.Detail = tc.detail
				plpgsqlError(t, p, c, "SELECT * FROM "+tc.name+"()", true, d)
			}
			userOperatorExec(t, p, b, c, "CREATE FUNCTION rn_unassigned() RETURNS SETOF rn_expected LANGUAGE plpgsql AS $$DECLARE r record; BEGIN RETURN NEXT r; END$$")
			d := plpgsqlRuntimeDiagnostic("55000", "record \"r\" is not assigned yet", "rn_unassigned()", "RETURN NEXT")
			d.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
			plpgsqlError(t, p, c, "SELECT * FROM rn_unassigned()", true, d)
			userOperatorExec(t, p, b, c, `CREATE TYPE rn_quoted AS("a""b" integer)`,
				`CREATE FUNCTION rn_quoted_column() RETURNS SETOF rn_quoted LANGUAGE plpgsql AS $$DECLARE r record; BEGIN SELECT 1::bigint INTO r; RETURN NEXT r; END$$`,
				`CREATE FUNCTION rn_quoted_record() RETURNS SETOF rn_expected LANGUAGE plpgsql AS $$DECLARE "a""b" record; BEGIN RETURN NEXT "a""b"; END$$`)
			d = plpgsqlRuntimeDiagnostic("42804", "wrong record type supplied in RETURN NEXT", "rn_quoted_column()", "RETURN NEXT")
			d.Detail = `Returned type bigint does not match expected type integer in column "a"b" (position 1).`
			plpgsqlError(t, p, c, "SELECT * FROM rn_quoted_column()", true, d)
			d = plpgsqlRuntimeDiagnostic("55000", `record "a"b" is not assigned yet`, "rn_quoted_record()", "RETURN NEXT")
			d.Detail = "The tuple structure of a not-yet-assigned record is indeterminate."
			plpgsqlError(t, p, c, "SELECT * FROM rn_quoted_record()", true, d)
		})
	})
	t.Run("admission", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TABLE rn_admit(id integer)")
			for _, tc := range []struct{ name, returns, body, message, hint string }{
				{"rn_nonset", "integer", "RETURN NEXT r;", "cannot use RETURN NEXT in a non-SETOF function", ""},
				{"rn_unreachable", "integer", "RETURN r; IF false THEN RETURN NEXT r; END IF;", "cannot use RETURN NEXT in a non-SETOF function", ""},
				{"rn_value_return", "SETOF rn_admit", "RETURN r;", "RETURN cannot have a parameter in function returning set", "Use RETURN NEXT or RETURN QUERY."},
			} {
				q := "CREATE FUNCTION " + tc.name + "() RETURNS " + tc.returns + " LANGUAGE plpgsql AS $$DECLARE r rn_admit; BEGIN " + tc.body + " END$$"
				d := fixture.ControlsReferenceDiagnostic("42804", tc.message)
				d.Hint = tc.hint
				// NEXT admission points at RETURN; ordinary value RETURN points at its operand.
				prefix := "CREATE FUNCTION " + tc.name + "() RETURNS " + tc.returns + " LANGUAGE plpgsql AS $$DECLARE r rn_admit; BEGIN "
				if tc.name == "rn_unreachable" {
					prefix += "RETURN r; IF false THEN "
				}
				d.Position = fixture.Position(len(prefix) + 1)
				if tc.name == "rn_value_return" {
					d.Position = fixture.Position(len(prefix) + len("RETURN ") + 1)
				}
				plpgsqlError(t, p, c, q, false, d)
			}
		})
	})
	t.Run("output_admission", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE TYPE rn_output_row AS(id integer,label text)")
			for _, tc := range []struct{ name, before string }{
				{"rn_output", ""},
				{"rn_output_unreachable", "RETURN; IF false THEN "},
			} {
				prefix := "CREATE FUNCTION " + tc.name + "() RETURNS TABLE(id integer,label text) LANGUAGE plpgsql AS $$DECLARE r rn_output_row; BEGIN " + tc.before
				q := prefix + "RETURN NEXT r;"
				if tc.before != "" {
					q += " END IF;"
				}
				q += " END$$"
				d := fixture.ControlsReferenceDiagnostic("42804", "RETURN NEXT cannot have a parameter in function with OUT parameters")
				// Output-parameter admission points at the explicit operand.
				d.Position = fixture.Position(len(prefix) + len("RETURN NEXT ") + 1)
				plpgsqlError(t, p, c, q, false, d)
			}
		})
	})
}
