package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSSLFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ssl_smoke_other"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		statsExec(t, p, b, o, "CREATE TABLE ssl_sentinel(v integer)", 0)
		statsExec(t, p, b, o, "INSERT INTO ssl_sentinel VALUES(37)", 1)
		statsExec(t, p, b, o, "CREATE ROLE ssl_smoke_other LOGIN PASSWORD 'role-attributes-fixture'", 0)
		a, other := statActivityConn(t, p, b), statActivityConn(t, p, b)
		apid, bpid := statActivityPID(t, p, a), statActivityPID(t, p, other)
		sslPair(t, p, b, o, apid, bpid)
		held := sslHeld(t, p, o, sslPairSQL)
		defer held.Statement.Close()
		sslPair(t, p, b, held, apid, bpid)
		sslPair(t, p, b, held, apid, bpid)
		db := fixture.RoleAttributesPool(t, p, b, "ssl_smoke_other")
		ordinary, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer ordinary.Close()
		sslWant(t, p, b, ordinary, apid, false)
		sslWant(t, p, b, ordinary, statActivityPID(t, p, ordinary), true)
		step := fixture.ErrorDiagnostic("INSERT INTO pg_catalog.pg_stat_ssl(pid) VALUES(1)", sslMutationDiagnostic("INSERT"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, o, b.Recorder, step)
		sslSentinel(t, p, b, o)
		sslPair(t, p, b, held, apid, bpid)
		fixture.StatisticsRetire(t, a)
		statActivityRetired(t, p, o, apid)
		fixture.StatisticsClearSnapshot(t, p, b, o)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, sslStep(sslPairSQL, b.PG, []int64{bpid}, apid, bpid))
		sslWant(t, p, b, o, apid, false)
		sslWant(t, p, b, o, bpid, true)
		sslSentinel(t, p, b, o)
	})
}
