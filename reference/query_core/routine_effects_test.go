package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// A SQL function is a scalar expression, not an effect/publication boundary.
// Auxiliary modifying work completes and is committed/rolled back by the root.
func routineStatementEffects(t *testing.T) {
	t.Helper()
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE TABLE routine_effects(x integer)",
			"CREATE TABLE routine_error_row(x integer)",
			"CREATE TABLE routine_effect_parent(id integer PRIMARY KEY)",
			"CREATE TABLE routine_effect_child(id integer REFERENCES routine_effect_parent(id))",
			"CREATE FUNCTION routine_effect(integer) RETURNS integer LANGUAGE SQL AS $$WITH inserted AS (INSERT INTO routine_effects VALUES($1),($1+1) RETURNING x) SELECT min(x) FROM inserted$$")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO routine_error_row VALUES(0)", 1))
		q := "SELECT routine_effect(4) AS a,routine_effect(8) AS b"
		s := fixture.Select(q, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(8)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		routineValue(t, p, b, c, "SELECT sum(x)::integer AS v FROM routine_effects", "v", "INT4", int64(52))
		// Force a runtime row-dependent error after a routine target, not a
		// planning-time constant error that could avoid invoking the routine.
		for _, prepared := range []bool{false, true} {
			q := "SELECT routine_effect(20),10/x FROM routine_error_row"
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		routineValue(t, p, b, c, "SELECT sum(x)::integer AS v FROM routine_effects", "v", "INT4", int64(52))
		// Root FK completion must retain its obligation across callee activation.
		rows, err := c.QueryContext(p.Ctx, "INSERT INTO routine_effect_child VALUES(999) RETURNING routine_effect(30)")
		if err == nil {
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		if err == nil {
			t.Fatal("routine activation discarded parent FK obligation")
		}
		fixture.State(t, err, "23503")
		routineValue(t, p, b, c, "SELECT sum(x)::integer AS v FROM routine_effects", "v", "INT4", int64(52))
		// The existing native COUNT owner declares non-nullability; SQL scalar
		// calls above intentionally do not inherit that aggregate metadata.
		routineValueWithNativeNullability(t, p, b, c, "SELECT count(*) AS v FROM routine_effect_child", "v", "INT8", int64(0), false, true)
	})
}
