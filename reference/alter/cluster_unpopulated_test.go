package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterUnpopulatedSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	persistenceExec(t, p, b, c, "CREATE TABLE cluster_source(k int)")
	persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO cluster_source VALUES(2),(1)", 2))
	persistenceExec(t, p, b, c, "CREATE MATERIALIZED VIEW cluster_empty AS SELECT k FROM cluster_source WITH NO DATA")
	for _, q := range []string{"CREATE INDEX cluster_empty_a ON cluster_empty(k)", "CREATE INDEX cluster_empty_b ON cluster_empty(k DESC)"} {
		persistenceExec(t, p, b, c, q)
	}
}

func clusterUnpopulatedReadError() fixture.Diagnostic {
	return *fixture.MaterializedViewUnpopulated("cluster_empty").Diagnostic
}

func clusterUnpopulatedState(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, a, selectedB, populated bool) {
	t.Helper()
	persistenceRun(t, p, b, c, clusterState("cluster_empty", [][]any{{"cluster_empty_a", a}, {"cluster_empty_b", selectedB}}))
	persistenceRun(t, p, b, c, fixture.Select("SELECT relispopulated FROM pg_catalog.pg_class WHERE oid='cluster_empty'::regclass", []string{"relispopulated"}, []string{"BOOL"}, [][]any{{populated}}))
	if populated {
		persistenceRun(t, p, b, c, fixture.Select("SELECT k FROM cluster_empty ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	} else {
		persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("SELECT k FROM cluster_empty", clusterUnpopulatedReadError()))
	}
	persistenceRun(t, p, b, c, fixture.Select("SELECT k FROM cluster_source ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
}

// A held statement is prepared before the lifecycle changes below. Preparation
// cannot freeze population or the remembered-index selection.
func clusterUnpopulatedExecutor(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string, queries ...string) func(fixture.Step) {
	t.Helper()
	held := make(map[string]*sql.Stmt)
	if mode == "held" {
		for _, q := range queries {
			held[q] = clusterCommandHeld(t, p, b, c, q)
		}
	}
	return func(step fixture.Step) {
		t.Helper()
		var x fixture.Executor = c
		if mode == "fresh" {
			step = fixture.Prepared(step)
		} else if mode == "held" {
			x = fixture.HeldStatement{Executor: c, Statement: held[step.Query], SQL: step.Query}
		}
		persistenceRun(t, p, b, x, step)
	}
}

func TestReferenceClusterUnpopulatedMaterializedView(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		for _, lifecycle := range []string{"created_no_data", "refresh_no_data"} {
			t.Run(mode+"/"+lifecycle, func(t *testing.T) {
				persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					clusterUnpopulatedSeed(t, p, b, c)
					run := clusterUnpopulatedExecutor(t, p, b, c, mode, "CLUSTER cluster_empty USING cluster_empty_a", "CLUSTER cluster_empty USING cluster_empty_b", "CLUSTER cluster_empty", "CLUSTER")
					if lifecycle == "refresh_no_data" {
						persistenceExec(t, p, b, c, "REFRESH MATERIALIZED VIEW cluster_empty")
						persistenceExec(t, p, b, c, "REFRESH MATERIALIZED VIEW cluster_empty WITH NO DATA")
					}
					clusterUnpopulatedState(t, p, b, c, false, false, false)
					run(fixture.Exec("CLUSTER cluster_empty USING cluster_empty_b", 0))
					clusterUnpopulatedState(t, p, b, c, false, false, false)
					run(fixture.Exec("CLUSTER", 0))
					clusterUnpopulatedState(t, p, b, c, false, false, false)
					persistenceExec(t, p, b, c, "ALTER MATERIALIZED VIEW cluster_empty CLUSTER ON cluster_empty_a")
					for _, q := range []string{"CLUSTER cluster_empty USING cluster_empty_b", "CLUSTER cluster_empty", "CLUSTER"} {
						run(fixture.Exec(q, 0))
						clusterUnpopulatedState(t, p, b, c, true, false, false)
					}
					persistenceExec(t, p, b, c, "REFRESH MATERIALIZED VIEW cluster_empty")
					clusterUnpopulatedState(t, p, b, c, true, false, true)
					run(fixture.Exec("CLUSTER cluster_empty USING cluster_empty_b", 0))
					clusterUnpopulatedState(t, p, b, c, false, true, true)
					persistenceExec(t, p, b, c, "REFRESH MATERIALIZED VIEW cluster_empty WITH NO DATA")
					for _, q := range []string{"CLUSTER cluster_empty USING cluster_empty_a", "CLUSTER cluster_empty", "CLUSTER"} {
						run(fixture.Exec(q, 0))
						clusterUnpopulatedState(t, p, b, c, false, true, false)
					}
					persistenceExec(t, p, b, c, "REFRESH MATERIALIZED VIEW cluster_empty")
					clusterUnpopulatedState(t, p, b, c, false, true, true)
				})
			})
		}
	}
}

func TestReferenceClusterUnpopulatedDiagnostics(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		for _, tc := range []struct{ name, query, code, message string }{
			{"no_preference", "CLUSTER cluster_empty", "42704", `there is no previously clustered index for table "cluster_empty"`},
			{"missing_index", "CLUSTER cluster_empty USING missing_idx", "42704", `index "missing_idx" for table "cluster_empty" does not exist`},
			{"nonindex", "CLUSTER cluster_empty USING cluster_source", "42809", `"cluster_source" is not an index`},
			{"wrong_parent", "CLUSTER cluster_empty USING other_idx", "42809", `"other_idx" is not an index for table "cluster_empty"`},
			{"hash", "CLUSTER cluster_empty USING hash_idx", "0A000", `cannot cluster on index "hash_idx" because access method does not support clustering`},
			{"partial", "CLUSTER cluster_empty USING partial_idx", "0A000", `cannot cluster on partial index "partial_idx"`},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					clusterUnpopulatedSeed(t, p, b, c)
					for _, q := range []string{"CREATE INDEX other_idx ON cluster_source(k)", "CREATE INDEX hash_idx ON cluster_empty USING hash(k)", "CREATE INDEX partial_idx ON cluster_empty(k) WHERE k>0"} {
						persistenceExec(t, p, b, c, q)
					}
					run := clusterUnpopulatedExecutor(t, p, b, c, mode, tc.query)
					step := fixture.ErrorDiagnostic(tc.query, persistenceDiagnostic(tc.code, tc.message))
					step.ErrorStage = "execute"
					run(step)
					persistenceRun(t, p, b, c, clusterState("cluster_empty", [][]any{{"cluster_empty_a", false}, {"cluster_empty_b", false}, {"hash_idx", false}, {"partial_idx", false}}))
					persistenceRun(t, p, b, c, fixture.Select("SELECT relispopulated FROM pg_class WHERE oid='cluster_empty'::regclass", []string{"relispopulated"}, []string{"BOOL"}, [][]any{{false}}))
					persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("SELECT k FROM cluster_empty", clusterUnpopulatedReadError()))
					persistenceRun(t, p, b, c, fixture.Select("SELECT k FROM cluster_source ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			})
		}
	}
}

func clusterUnpopulatedLockInventory(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, heap int64) {
	t.Helper()
	persistenceRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_locks WHERE locktype='relation' AND pid=pg_backend_pid() AND relation='cluster_empty'::regclass AND mode='AccessExclusiveLock' AND granted", []string{"n"}, []string{"INT8"}, [][]any{{heap}}))
}

// Successful unpopulated skips restore heap admission without losing prior
// heap holds. Physical selected-index/planner grants are not simulated.
func TestReferenceClusterUnpopulatedLocks(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		for _, form := range []string{"named", "bare", "preexisting_heap", "sibling_wait"} {
			t.Run(mode+"/"+form, func(t *testing.T) {
				persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					clusterUnpopulatedSeed(t, p, b, c)
					persistenceExec(t, p, b, c, "ALTER MATERIALIZED VIEW cluster_empty CLUSTER ON cluster_empty_a")
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					defer peer.ExecContext(p.Ctx, "ROLLBACK")
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					persistenceExec(t, p, b, peer, "SET lock_timeout='20ms'")
					q := "CLUSTER cluster_empty USING cluster_empty_a"
					if form == "bare" {
						q = "CLUSTER"
					}
					run := clusterUnpopulatedExecutor(t, p, b, c, mode, q)
					if form == "sibling_wait" {
						persistenceExec(t, p, b, peer, "BEGIN")
						persistenceExec(t, p, b, peer, "ALTER INDEX cluster_empty_b RENAME TO cluster_empty_pending")
						persistenceExec(t, p, b, c, "SET lock_timeout='20ms'")
						run(fixture.Exec(q, 0))
						persistenceExec(t, p, b, peer, "ROLLBACK")
						clusterUnpopulatedState(t, p, b, c, true, false, false)
						return
					}
					if form != "bare" {
						persistenceExec(t, p, b, c, "BEGIN")
					}
					if form == "preexisting_heap" {
						persistenceExec(t, p, b, c, "REFRESH MATERIALIZED VIEW cluster_empty WITH NO DATA")
					}
					run(fixture.Exec(q, 0))
					heap := int64(0)
					if form == "preexisting_heap" {
						heap = 1
					}
					clusterUnpopulatedLockInventory(t, p, b, c, heap)
					if form == "preexisting_heap" {
						want := persistenceDiagnostic("55P03", "canceling statement due to lock timeout")
						want.Position = fixture.Position(15)
						persistenceRun(t, p, b, peer, fixture.ErrorDiagnostic("SELECT k FROM cluster_empty", want))
					} else if form == "bare" {
						persistenceRun(t, p, b, peer, fixture.ErrorDiagnostic("SELECT k FROM cluster_empty", clusterUnpopulatedReadError()))
					}
					if form != "preexisting_heap" {
						persistenceExec(t, p, b, peer, "BEGIN")
						persistenceExec(t, p, b, peer, "ALTER INDEX cluster_empty_b RENAME TO cluster_empty_probe")
						persistenceExec(t, p, b, peer, "ROLLBACK")
					}
					if form != "bare" {
						persistenceExec(t, p, b, c, "COMMIT")
					}
					clusterUnpopulatedLockInventory(t, p, b, c, 0)
					persistenceRun(t, p, b, peer, fixture.ErrorDiagnostic("SELECT k FROM cluster_empty", clusterUnpopulatedReadError()))
					persistenceExec(t, p, b, peer, "BEGIN")
					persistenceExec(t, p, b, peer, "ALTER INDEX cluster_empty_a RENAME TO cluster_empty_probe")
					persistenceExec(t, p, b, peer, "ROLLBACK")
					clusterUnpopulatedState(t, p, b, c, true, false, false)
				})
			})
		}
	}
}
