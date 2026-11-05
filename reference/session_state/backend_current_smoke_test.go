package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendCurrentFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bc_smoke_observer"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		admin := statActivityConn(t, p, b)
		peer := statActivityConn(t, p, b)
		pid := statActivityPID(t, p, peer)
		visible := backendQuery(t, p, b.PG, o, 0, pid)
		if len(visible) != 1 || visible[0][4] != "idle" {
			t.Fatal("actual idle activity", visible)
		}
		backendIOCheck(t, backendQuery(t, p, b.PG, o, 1, pid), b.PG)
		backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, pid), b.PG, true)
		var count, nonnull int64
		if err := o.QueryRowContext(p.Ctx, "SELECT count(*),count(wal_records) FROM pg_stat_get_backend_wal(0)").Scan(&count, &nonnull); err != nil || count != 1 || nonnull != 0 {
			t.Fatal("scalar absent cardinality", count, nonnull, err)
		}
		activityExec(t, p, o, "CREATE ROLE bc_smoke_observer LOGIN PASSWORD 'role-attributes-fixture'")
		pool := fixture.RoleAttributesPool(t, p, b, "bc_smoke_observer")
		observer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		backendMasked(t, visible[0], backendQuery(t, p, b.PG, observer, 0, pid)[0])
		backendIOCheck(t, backendQuery(t, p, b.PG, observer, 1, pid), b.PG)
		backendWALCheck(t, backendQuery(t, p, b.PG, observer, 2, pid), b.PG, true)
		activityExec(t, p, o, "SET stats_fetch_consistency=cache")
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		old := backendStamp(t, p, b.PG, o, pid, true)
		lo, hi := backendReset(t, p, admin, pid)
		if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), old) {
			t.Fatal("IO/WAL split cache")
		}
		newcomer := statActivityConn(t, p, b)
		newPID := statActivityPID(t, p, newcomer)
		if len(backendQuery(t, p, b.PG, o, 1, newPID)) != 0 {
			t.Fatal("new PID entered retained cut")
		}
		statActivityClear(t, p, o)
		stamp := backendBound(t, backendStamp(t, p, b.PG, o, pid, true), lo, hi)
		if !reflect.DeepEqual(backendStamp(t, p, b.PG, o, pid, false), stamp) {
			t.Fatal("clear split reset")
		}
		backendIOCheck(t, backendQuery(t, p, b.PG, o, 1, newPID), b.PG)
		statActivitySignal(t, p, admin, pid, true)
		statActivityRetired(t, p, admin, pid)
		if len(backendQuery(t, p, b.PG, o, 1, pid)) != 0 {
			t.Fatal("ended PID retained metrics")
		}
		backendWALCheck(t, backendQuery(t, p, b.PG, o, 2, pid), b.PG, false)
		activityExec(t, p, o, "ROLLBACK")
		step := fixture.ErrorDiagnostic("SELECT pg_stat_get_backend_wal(true)", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function pg_stat_get_backend_wal(boolean) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "8"})
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
	})
}
