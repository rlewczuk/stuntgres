package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendStateFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bs_smoke_other"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		peer := statActivityConn(t, p, b)
		mapped := stateMapped(t, p, b, o, peer)
		id := mapped[0]
		held := statePrepare(t, p, o)
		activityExec(t, p, o, "CREATE ROLE bs_smoke_other LOGIN PASSWORD 'role-attributes-fixture'")
		activityExec(t, p, o, "CREATE TABLE bs_smoke_data(v integer)")
		activityExec(t, p, o, "CREATE TABLE bs_smoke_wait(v integer)")
		activityExec(t, p, peer, "BEGIN")
		defer peer.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, peer, "SAVEPOINT parent")
		activityExec(t, p, peer, "INSERT INTO bs_smoke_data VALUES(1)")
		stateSub(t, p, b, o, id, 1, false, true)
		row := stateRead(t, p, b, held, id)
		if row[1] == nil || row[2] == nil || row[5] != "(1,f)" {
			t.Fatal("real query/transaction/cache", row)
		}
		activityExec(t, p, peer, "SAVEPOINT child")
		activityExec(t, p, peer, "INSERT INTO bs_smoke_data VALUES(2)")
		stateSub(t, p, b, o, id, 2, false, true)
		activityExec(t, p, peer, "RELEASE child")
		stateSub(t, p, b, o, id, 2, false, true)
		activityExec(t, p, peer, "ROLLBACK TO parent")
		stateSub(t, p, b, o, id, 0, false, true)
		activityExec(t, p, peer, "INSERT INTO bs_smoke_data VALUES(3)")
		stateSub(t, p, b, o, id, 1, false, true)
		activityExec(t, p, peer, "RELEASE parent")
		stateSub(t, p, b, o, id, 1, false, true)
		stateLockWitness(t, p, b, o, peer, id, "bs_smoke_wait", func(visible []any) {
			pool := fixture.RoleAttributesPool(t, p, b, "bs_smoke_other")
			other, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			stateMask(t, stateRead(t, p, b, other, id), visible, false)
			stateMask(t, stateRead(t, p, b, statePrepare(t, p, other), id), visible, false)
		})
		activityExec(t, p, peer, "ROLLBACK")
		statActivityClear(t, p, o)
		row = stateRead(t, p, b, held, id)
		if row[2] != nil || row[5] != "(0,f)" || row[0] != "ROLLBACK" {
			t.Fatal("fresh completed state", row)
		}
		stateIdle(t, b.PG, row)
		stateAbsent(t, p, b, o, -1)
		stateBoth(t, p, b, o, fixture.Select("SELECT pg_stat_get_backend_subxact(NULL)::text AS strict,pg_stat_get_backend_subxact(-1)::text AS missing", []string{"strict", "missing"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "(,)"}}))
		q := "SELECT pg_stat_get_backend_activity(true)"
		step := fixture.ErrorDiagnostic(q, identityDiagnostic(q, stateFunctions[0], "42883", "function pg_stat_get_backend_activity(boolean) does not exist", identityNoMatchHint))
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
		step.Prepared = true
		step.ErrorStage = "prepare"
		fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
		if backendCount(t, p, peer, "SELECT count(*) FROM bs_smoke_data") != 0 {
			t.Fatal("canceled top transaction leaked writes")
		}
	})
}
