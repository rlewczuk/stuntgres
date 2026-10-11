package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func discardReborrow(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) *sql.Conn {
	t.Helper()
	identity := settingsPhysical(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reused, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reused.Close() })
	if settingsPhysical(t, reused) != identity {
		t.Fatal("physical replacement is not evidence of DISCARD or healthy reuse")
	}
	return reused
}

func TestReferenceDiscardHealthyReuse(t *testing.T) {
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		discardSeed(t, p, b, a, peer)
		a = discardReborrow(t, p, b, a)
		discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(7))
		discardScalar(t, p, b, a, "SELECT v FROM discard_t", "v", "INT4", int64(7))
		discardScalar(t, p, b, a, "SHOW application_name", "application_name", "TEXT", "discard_7")
		discardScalar(t, p, b, a, "SELECT currval('discard_s')", "currval", "INT8", int64(1))
		discardScalar(t, p, b, peer, "SELECT pg_try_advisory_lock(81001::bigint)", "pg_try_advisory_lock", "BOOL", false)
		discardPeerIntact(t, p, b, peer)
		discardExec(t, p, b, a, "DISCARD ALL")
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestReferenceDiscardPoolExplicitCleanup(t *testing.T) {
	discardScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		var app string
		if err := a.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&app); err != nil {
			t.Fatal(err)
		}
		discardSeed(t, p, b, a, peer)
		discardExec(t, p, b, a, "DISCARD ALL")
		a = discardReborrow(t, p, b, a)
		discardScalar(t, p, b, a, "SHOW application_name", "application_name", "TEXT", app)
		sqlPreparedStep(t, p, b, a, fixture.ErrorDiagnostic("EXECUTE p", sqlPreparedMissing("p")))
		discardMissingTable(t, p, b, a, "discard_t")
		discardMissingMemory(t, p, b, a, "discard_s")
		discardScalar(t, p, b, peer, "SELECT pg_try_advisory_lock(81001::bigint)", "pg_try_advisory_lock", "BOOL", true)
		discardPeerIntact(t, p, b, peer)
		discardExec(t, p, b, a, "PREPARE p AS SELECT 17 AS v")
		discardScalar(t, p, b, a, "EXECUTE p", "v", "INT4", int64(17))
		discardExec(t, p, b, a, "CREATE TEMP TABLE discard_t(v int)")
		discardScalar(t, p, b, a, "SELECT nextval('discard_s')", "nextval", "INT8", int64(21))
		discardExec(t, p, b, a, "DISCARD ALL")
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
