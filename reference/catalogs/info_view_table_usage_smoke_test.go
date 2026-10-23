package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewTableUsageFeatureSmoke(t *testing.T) {
	vtuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vtuSeed(run)
		where := "view_schema='vtu_scope'"
		check := func(want [][]any) {
			s := vtuProjection(b.PG, where, want)
			run(s)
			run(fixture.Prepared(s))
		}
		check(vtuSeedRows(database))
		empty := vtuProjection(b.PG, where+" AND table_name=$1", nil)
		empty.Args = []any{nil}
		run(empty)
		run(fixture.Prepared(empty))
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = "8"
		errStep := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.view_table_usage", d)
		errStep.SelectRows, errStep.ErrorStage = true, "execute"
		run(errStep)
		errStep.Prepared, errStep.ErrorStage = true, "prepare"
		run(errStep)
		// Isolate explicit ACLs from the supported native dump seed.
		for _, q := range []string{
			"CREATE ROLE vtu_b", "CREATE ROLE vtu_reader", "CREATE SCHEMA vtu_private",
			"REVOKE ALL ON SCHEMA vtu_private FROM PUBLIC", "CREATE TABLE vtu_private.t(a integer)",
			"CREATE VIEW vtu_private.v AS SELECT a FROM vtu_private.t", "ALTER TABLE vtu_private.t OWNER TO vtu_b",
			"GRANT SELECT ON vtu_private.t,vtu_private.v TO vtu_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		for _, actor := range []struct {
			role string
			rows [][]any
		}{
			{"vtu_b", [][]any{vtuRow(database, "vtu_private", "v", "vtu_private", "t")}},
			{"vtu_reader", nil},
		} {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+actor.role, 0))
			s := vtuProjection(b.PG, "view_schema='vtu_private'", actor.rows)
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		run(fixture.Exec("DROP SCHEMA vtu_private CASCADE", 0))
		run(fixture.Exec("CREATE OR REPLACE VIEW vtu_scope.v AS SELECT a,a AS b,a AS again FROM vtu_scope.u", 0))
		want := [][]any{vtuRow(database, "vtu_scope", "v", "vtu_scope", "u"), vtuRow(database, "vtu_scope", "w", "vtu_scope", "v")}
		check(want)
		run(fixture.Exec("ALTER TABLE vtu_scope.u RENAME TO renamed_u", 0))
		want[0] = vtuRow(database, "vtu_scope", "v", "vtu_scope", "renamed_u")
		check(want)
		vtuRoundTrip(t, p, b, c, r, where, want, func() { check(want) })
		run(fixture.Exec("DROP VIEW vtu_scope.w", 0))
		check(want[:1])
		run(fixture.Exec("DROP VIEW vtu_scope.v", 0))
		check(nil)
	})
}
