package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLPreparedFeatureSmoke(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)
	}{
		{"fresh_values", sqlPreparedValues},
		{"session_ownership", sqlPreparedOwnership},
		{"zero_formal_no_evaluation", sqlPreparedZero},
		{"go_handle_effect_phases", sqlPreparedAdapter},
		{"full_diagnostics", sqlPreparedSmokeDiagnostics},
	} {
		t.Run(tc.name, func(t *testing.T) { sqlPreparedScenario(t, tc.body) })
	}
	for _, form := range []string{"update", "delete", "merge"} {
		t.Run(form, func(t *testing.T) {
			sqlPreparedScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) { sqlPreparedForm(t, p, b, c, form) })
		})
	}
}

func sqlPreparedSmokeDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	sqlPreparedStep(t, p, b, c, fixture.Exec("CREATE TABLE prep_smoke_sentinel(v int)", 0))
	sqlPreparedStep(t, p, b, c, fixture.Exec("INSERT INTO prep_smoke_sentinel VALUES(7)", 1))
	sqlPreparedStep(t, p, b, c, fixture.Exec("PREPARE p(int) AS SELECT $1", 0))
	sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("PREPARE p(int) AS SELECT $1", sqlPreparedDiagnostic("42P05", `prepared statement "p" already exists`)))
	d := sqlPreparedDiagnostic("42601", `wrong number of parameters for prepared statement "p"`)
	d.Detail = "Expected 1 parameters but got 0."
	sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p", d))
	d = sqlPreparedDiagnostic("42804", "parameter $1 of type boolean cannot be coerced to the expected type integer")
	d.Hint = "You will need to rewrite or cast the expression."
	d.Position = "11"
	sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p(TRUE)", d))
	sqlPreparedStep(t, p, b, c, fixture.Exec("DEALLOCATE p", 0))
	sqlPreparedStep(t, p, b, c, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
	sqlPreparedStep(t, p, b, c, sqlPreparedScalar("SELECT v FROM prep_smoke_sentinel", "v", "INT4", int64(7)))
}
