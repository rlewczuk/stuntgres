package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Transient build phases have deterministic callback barriers in core tests.
// These public controls exercise the already-supported SQL paths and demand
// terminal cleanup without inventing a large-workload sampling guarantee.
func TestReferenceCreateIndexProgressSupportedBuilds(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		copyProgressExec(t, p.Ctx, c, "CREATE TABLE index_progress_supported(n INT,s TEXT)")
		copyProgressExec(t, p.Ctx, c, "INSERT INTO index_progress_supported VALUES(1,'A'),(2,'B'),(NULL,NULL)")
		id := copyProgressIdentify(t, p, c, "index_progress_supported")
		for i, q := range []string{
			"CREATE INDEX index_progress_expression ON index_progress_supported(lower(s),(n+1)) WHERE n>0",
			"CREATE UNIQUE INDEX index_progress_unique ON index_progress_supported(n)",
			"CREATE INDEX index_progress_hash ON index_progress_supported USING hash(n)",
			"CREATE INDEX index_progress_brin ON index_progress_supported USING brin(n)",
			"REINDEX INDEX index_progress_expression",
			"REINDEX TABLE index_progress_supported",
			"CREATE INDEX IF NOT EXISTS index_progress_hash ON index_progress_supported USING hash(n)",
		} {
			s := fixture.Exec(q, 0)
			if i%2 != 0 {
				s = fixture.Prepared(s)
			}
			vacuumRun(t, p, b, c, s)
			indexProgressIdle(t, p, c, id.pid)
		}
		vacuumRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_index WHERE indrelid='index_progress_supported'::regclass AND indisvalid AND indisready", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
		vacuumRun(t, p, b, c, fixture.Select("SELECT n,s FROM index_progress_supported ORDER BY n", []string{"n", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "B"}, {nil, nil}}))
		for _, q := range []string{
			"CREATE TABLE index_progress_parent(n INT) PARTITION BY RANGE(n)",
			"CREATE TABLE index_progress_child PARTITION OF index_progress_parent FOR VALUES FROM(0) TO(10)",
			"CREATE UNIQUE INDEX index_progress_child_existing ON index_progress_child(n)",
			"CREATE UNIQUE INDEX index_progress_partitioned ON index_progress_parent(n)",
		} {
			vacuumRun(t, p, b, c, fixture.Exec(q, 0))
			indexProgressIdle(t, p, c, id.pid)
		}
		vacuumRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_index WHERE indrelid='index_progress_parent'::regclass AND indisvalid", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		copyProgressExec(t, p.Ctx, c, "CREATE TABLE index_progress_failure(n INT)")
		copyProgressExec(t, p.Ctx, c, "INSERT INTO index_progress_failure VALUES(1),(1)")
		want := fixture.ConcurrentIndexUnique("index_progress_failure", "index_progress_failure_ix", "Key (n)=(1) is duplicated.", true)
		vacuumRun(t, p, b, c, fixture.ImplicitError("CREATE UNIQUE INDEX index_progress_failure_ix ON index_progress_failure(n)", want))
		indexProgressIdle(t, p, c, id.pid)
		if _, found := fixture.ConcurrentIndexState(t, p, b, "index_progress_failure_ix"); found {
			t.Fatal("failed ordinary CREATE published index")
		}
	})
}
