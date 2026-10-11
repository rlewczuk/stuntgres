package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDiscardFeatureSmoke(t *testing.T) {
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		var app string
		if err := a.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&app); err != nil {
			t.Fatal(err)
		}
		discardSeed(t, p, b, a, peer)
		discardExec(t, p, b, a, "DISCARD PLANS")
		discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
		discardExec(t, p, b, a, "DISCARD SEQUENCES")
		discardMissingMemory(t, p, b, a, "discard_s")
		discardScalar(t, p, b, peer, "SELECT nextval('discard_s')", "nextval", "INT8", int64(12))
		discardScalar(t, p, b, a, "SELECT nextval('discard_s')", "nextval", "INT8", int64(21))
		discardExec(t, p, b, a, "DISCARD TEMP")
		discardMissingTable(t, p, b, a, "discard_t")
		discardScalar(t, p, b, a, "SELECT v FROM discard_durable", "v", "INT4", int64(9))
		discardScalar(t, p, b, a, "SELECT currval('discard_s')", "currval", "INT8", int64(21))
		discardExec(t, p, b, a, "BEGIN")
		discardError(t, p, b, a, "DISCARD ALL", "25001", "DISCARD ALL cannot run inside a transaction block", "")
		discardExec(t, p, b, a, "ROLLBACK")
		discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
		discardExec(t, p, b, a, "DISCARD ALL")
		a = discardReborrow(t, p, b, a)
		discardScalar(t, p, b, a, "SHOW application_name", "application_name", "TEXT", app)
		sqlPreparedStep(t, p, b, a, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
		discardMissingMemory(t, p, b, a, "discard_s")
		discardScalar(t, p, b, peer, "EXECUTE p", "v", "INT4", int64(8))
		discardScalar(t, p, b, peer, "SELECT v FROM discard_t", "v", "INT4", int64(8))
		discardScalar(t, p, b, peer, "SELECT pg_try_advisory_lock(81001::bigint)", "pg_try_advisory_lock", "BOOL", true)
		discardExec(t, p, b, a, "PREPARE p AS SELECT 17 AS v")
		discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(17))
		discardExec(t, p, b, a, "DISCARD ALL")
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
