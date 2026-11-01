package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBloomFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bloom_control_owner", "bloom_control_reader"})
	fixture.SchemaReferenceCleanup(t, "bloom_portable")
	var emitted string
	bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := bloomRun(t, p, b, c)
		bloomControlRoles(run)
		bloomControlSeed(run)
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.t WHERE i=7 AND s='a'", [][]any{{int64(7), "a"}, {int64(7), "a"}}))
		held := bloomHeld(t, p, c, "SELECT i,s FROM bloom_portable.t WHERE i=$1")
		for _, tc := range []struct {
			arg  any
			want [][]any
		}{
			{7, [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}}},
			{8, [][]any{{int64(8), "b"}, {int64(8), "a"}}},
			{nil, nil},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, tc.want, tc.arg))
		}
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_portable.t WHERE i=7 OR s='a'", [][]any{{int64(7), "a"}, {int64(7), "a"}, {int64(7), nil}, {nil, "a"}, {int64(8), "a"}}))
		bloomCollisionSeed(run, "bloom_portable.smoke_collision")
		bloomCollisionChecks(t, p, b, c, "bloom_portable.smoke_collision")
		bloomDML(run, b, "bloom_portable.t")
		bloomControlRoundtrip(t, p, b, c, &emitted)
		// Reuse a statement prepared before DML and replacement, not merely a
		// fresh Prepare after Restore. Post-restore INSERT must be searchable.
		run(fixture.Exec("INSERT INTO bloom_portable.t VALUES(10,'q')", 1))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, [][]any{{int64(10), "q"}}, 10))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bloomSelect(b, held.SQL, nil, 7))
	})
}
