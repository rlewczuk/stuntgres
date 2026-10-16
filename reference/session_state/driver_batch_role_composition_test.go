package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func batchRoleComposition(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"batch_private", "batch_member"})
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		var identity string
		if err := a.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&identity); err != nil {
			t.Fatal(err)
		}
		q := "CREATE ROLE batch_private LOGIN; CREATE ROLE batch_member LOGIN; GRANT batch_private TO batch_member; GRANT SELECT ON batch_t TO batch_private; SET ROLE batch_member; SELECT i FROM batch_t"
		if b.PG {
			q = "GRANT USAGE ON SCHEMA public TO PUBLIC; " + q
		}
		failure := q + "; SELECT missing"
		w := batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(failure, "missing")
		r := batchQuery(t, p, a, failure)
		fixture.BatchReadSet(t, r, fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}, false})
		fixture.AssertDiagnostic(t, r.Err(), &w)
		r.Close()
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT current_user AS v", []string{"v"}, []string{"NAME"}, [][]any{{identity}}))
		for _, query := range []string{
			"SELECT count(*) AS v FROM pg_user WHERE usename='batch_private'",
			"SELECT count(*) AS v FROM pg_user WHERE usename='batch_member'",
		} {
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select(query, []string{"v"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		fixture.BatchSets(t, batchQuery(t, p, a, q), fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}, false})
		batchExec(t, p, b, a, "RESET ROLE", 0)
		batchExec(t, p, b, peer, "SET SESSION AUTHORIZATION batch_member", 0)
		batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
		batchExec(t, p, b, peer, "SET ROLE batch_private", 0)
		batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
		batchExec(t, p, b, peer, "RESET SESSION AUTHORIZATION; RESET ROLE", 0)
	})
}
