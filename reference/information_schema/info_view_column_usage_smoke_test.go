package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoViewColumnUsageFeatureSmoke(t *testing.T) {
	vcuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		vcuSeed(run)
		where := "view_schema='vcu_scope'"
		held := vcuHeld(t, p, c, r, vcuProjection(b.PG, where, nil).Query)
		check := func(want [][]any) {
			s := vcuProjection(b.PG, where, want)
			run(s)
			run(fixture.Prepared(s))
			held(s)
		}
		check(vcuSeedRows(database))
		empty := vcuProjection(b.PG, where+" AND column_name=$1", nil)
		empty.Args = []any{nil}
		run(empty)
		run(fixture.Prepared(empty))
		run(fixture.Exec("ALTER TABLE vcu_scope.t RENAME COLUMN b TO label", 0))
		want := vcuSeedRows(database)
		want[1] = vcuRow(database, "vcu_scope", "v", "vcu_scope", "t", "label")
		check(want)
		run(fixture.Exec("CREATE ROLE vcu_b", 0))
		run(fixture.Exec("CREATE ROLE vcu_reader", 0))
		run(fixture.Exec("ALTER TABLE vcu_scope.t OWNER TO vcu_b", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vcu_b", 0))
		check([][]any{vcuRow(database, "vcu_scope", "v", "vcu_scope", "t", "a"), vcuRow(database, "vcu_scope", "v", "vcu_scope", "t", "label")})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION vcu_reader", 0))
		check(nil)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("CREATE OR REPLACE VIEW vcu_scope.v AS SELECT a,a AS b,a AS again FROM vcu_scope.u", 0))
		want = [][]any{vcuRow(database, "vcu_scope", "v", "vcu_scope", "u", "a"), vcuRow(database, "vcu_scope", "w", "vcu_scope", "v", "a")}
		check(want)
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = "8"
		errStep := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.view_column_usage", d)
		errStep.SelectRows, errStep.ErrorStage = true, "execute"
		run(errStep)
		errStep.Prepared, errStep.ErrorStage = true, "prepare"
		run(errStep)
		replaced := false
		vcuRoundTrip(t, p, b, c, r, where, want, nil, func() {
			if !replaced {
				// Reset/Restore retires catalog-owned domain signatures, not
				// already returned Rows. Assert that fence, then hold a fresh
				// statement for this lifetime and the remaining mutations.
				stale := fixture.ErrorDiagnostic(vcuProjection(false, where, nil).Query, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
				stale.SelectRows, stale.ErrorStage = true, "execute"
				held(stale)
				held = vcuHeld(t, p, c, r, stale.Query)
				replaced = true
			}
			check(want)
		})
		run(fixture.Exec("DROP VIEW vcu_scope.v CASCADE", 0))
		check(nil)
	})
}
