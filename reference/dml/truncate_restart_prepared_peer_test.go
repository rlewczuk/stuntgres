package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTruncateRestartPreparedPeerRollback(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "restart_peer_a", "restart_peer_b")
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		for _, q := range []string{
			"CREATE SCHEMA restart_peer_a", "CREATE SCHEMA restart_peer_b",
			"CREATE TABLE restart_peer_b.t(id INT)",
			"CREATE SEQUENCE restart_peer_b.s START 10 OWNED BY restart_peer_b.t.id",
			"CREATE SEQUENCE restart_peer_b.other START 20",
		} {
			truncateExec(t, f, f.A, q, 0)
		}
		truncateExec(t, f, f.B, "SET search_path TO restart_peer_a,restart_peer_b", 0)
		query := "SELECT nextval('s') AS first,nextval('other') AS second"
		consumer := truncateRestartRoute(t, p, f.B, query, true)
		truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(10), int64(20)}}))
		truncateExec(t, f, f.A, "CREATE SEQUENCE restart_peer_a.s START 100", 0)
		truncateExec(t, f, f.A, "CREATE SEQUENCE restart_peer_a.other START 200", 0)
		truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(11), int64(21)}}))
		q := "TRUNCATE restart_peer_b.t RESTART IDENTITY"
		reset := truncateRestartRoute(t, p, f.A, q, held)
		defer f.A.ExecContext(p.Ctx, "ROLLBACK")
		truncateExec(t, f, f.A, "BEGIN", 0)
		truncateExec(t, f, reset, q, 0)
		// B has no sequence function, row read or held consumer demand while
		// A owns the private replacement. Do not infer peer invalidation from
		// the separately observed initiating-session rollback witness.
		truncateExec(t, f, f.A, "ROLLBACK", 0)
		// Pinned PostgreSQL: abort processes A's local invalidations but
		// does not publish the reverted rewrite to B.
		truncateRun(t, f, consumer, fixture.Select(query, []string{"first", "second"}, []string{"INT8", "INT8"}, [][]any{{int64(12), int64(22)}}))
		truncateRestartValue(t, f, f.A, "SELECT nextval('restart_peer_b.s')", "nextval", 13)
		truncateExec(t, f, f.B, "SET search_path TO public", 0)
		truncateExec(t, f, f.A, "DROP SCHEMA restart_peer_a,restart_peer_b CASCADE", 0)
		*f.Notices = nil
	})
}
