package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expression edge cases share the functional families rather than duplicating
// their coverage in a permanent smoke wrapper.
func plpgsqlExpressionCases(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name, body, code, message string
		value                     any
	}{
		{"empty_subquery", "BEGIN RETURN (SELECT x FROM pl_expr_source); END", "", "", nil},
		{"multirow", "BEGIN RETURN (SELECT x FROM pl_expr_source); END", "21000", "more than one row returned by a subquery used as an expression", nil},
		{"domain", "DECLARE x pl_positive; BEGIN x := -1; RETURN x; END", "23514", "value for domain pl_positive violates check constraint \"pl_positive_check\"", nil},
		{"return_io", "DECLARE s text := '42'; BEGIN RETURN s; END", "", "", int64(42)},
		{"ambiguous", "DECLARE x integer := 7; BEGIN RETURN (SELECT x FROM pl_expr_source); END", "42702", "column reference \"x\" is ambiguous", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE TABLE pl_expr_source(x integer)", "CREATE DOMAIN pl_positive AS integer CHECK(VALUE>0)")
				if tc.name == "multirow" {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pl_expr_source VALUES(1),(2)", 2))
				}
				userOperatorExec(t, p, b, c, "CREATE FUNCTION pl_expr() RETURNS integer LANGUAGE plpgsql AS $$"+tc.body+"$$")
				if tc.code == "" {
					plpgsqlValue(t, p, b, c, "SELECT pl_expr() AS v", "INT4", tc.value)
				} else {
					want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "pl_expr()", "RETURN")
					if tc.name == "domain" {
						want.Constraint = "pl_positive_check"
						want.DataType = "pl_positive"
						want.Schema = "public"
						want.Context = "PL/pgSQL function pl_expr() line 1 at assignment"
					}
					if tc.name == "ambiguous" {
						want.Detail = "It could refer to either a PL/pgSQL variable or a table column."
						want.InternalPosition = "9"
						want.InternalQuery = "(SELECT x FROM pl_expr_source)"
					}
					if tc.name == "multirow" {
						want.Context = "PL/pgSQL expression \"(SELECT x FROM pl_expr_source)\"\n" + want.Context
					}
					plpgsqlError(t, p, c, "SELECT pl_expr()", true, want)
				}
			})
		})
	}
}
