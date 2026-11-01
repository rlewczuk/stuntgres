package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewRoutineUsageFeatureSmoke(t *testing.T) {
	vruScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vruSeed(run)
		where := "table_schema='vru_scope'"
		held := vruHeld(t, p, c, r, vruProjection(b.PG, where, nil).Query)
		check := func(want [][]any) { s := vruProjection(b.PG, where, want); run(s); run(fixture.Prepared(s)); held(s) }
		check(vruSeedRows(t, p, c, database))
		empty := vruProjection(b.PG, where+" AND specific_name=$1", nil)
		empty.Args = []any{nil}
		run(empty)
		run(fixture.Prepared(empty))
		for _, q := range []string{
			"CREATE ROLE vru_b", "CREATE ROLE vru_reader", "CREATE ROLE vru_a",
			"ALTER FUNCTION vru_scope.f() OWNER TO vru_b", "ALTER FUNCTION vru_scope.g() OWNER TO vru_b",
			"ALTER VIEW vru_scope.v OWNER TO vru_a", "REVOKE ALL ON vru_scope.v FROM PUBLIC",
			"GRANT EXECUTE ON FUNCTION vru_scope.f(),vru_scope.g() TO vru_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		want := vruSeedRows(t, p, c, database)
		run(fixture.Exec("SET SESSION AUTHORIZATION vru_b", 0))
		check(want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, role := range []string{"vru_a", "vru_reader"} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			check(nil)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("CREATE OR REPLACE VIEW vru_scope.v AS SELECT vru_scope.g() AS x", 0))
		current := func() [][]any {
			return [][]any{vruRow(database, "vru_scope", "v", "vru_scope", rsuIdentity(t, p, c, "vru_scope", "g", ""))}
		}
		want = current()
		check(want)
		vruMissingColumn(t, p, c, r)
		refreshed := false
		vruRoundTrip(t, p, b, c, r, where, want, nil, func() {
			if !refreshed {
				fence := fixture.ErrorDiagnostic(vruProjection(false, where, nil).Query, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
				fence.SelectRows, fence.ErrorStage = true, "execute"
				held(fence)
				held = vruHeld(t, p, c, r, fence.Query)
				refreshed = true
			}
			check(current())
		})
		run(fixture.Exec("DROP VIEW vru_scope.v", 0))
		check(nil)
	})
}
