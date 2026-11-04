package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var clusterCommandProgressColumns = []string{"pid", "datid", "datname", "relid", "command", "phase", "cluster_index_relid", "heap_tuples_scanned", "heap_tuples_written", "heap_blks_total", "heap_blks_scanned", "index_rebuild_count"}
var clusterCommandProgressTypes = []string{"INT4", "OID", "NAME", "OID", "TEXT", "TEXT", "OID", "INT8", "INT8", "INT8", "INT8", "INT8"}

func clusterCommandProgressIdle() fixture.Step {
	return fixture.Select("SELECT * FROM pg_catalog.pg_stat_progress_cluster", clusterCommandProgressColumns, clusterCommandProgressTypes, [][]any{})
}

func TestReferenceClusterProgressSchema(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		persistenceRun(t, p, b, c, clusterCommandProgressIdle())
		persistenceRun(t, p, b, c, fixture.Prepared(clusterCommandProgressIdle()))
		persistenceRun(t, p, b, c, fixture.Select("SELECT relkind::text AS kind FROM pg_catalog.pg_class WHERE oid='pg_catalog.pg_stat_progress_cluster'::regclass", []string{"kind"}, []string{"TEXT"}, [][]any{{"v"}}))
		clusterCommandSeed(t, p, b, c)
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		observer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		held := clusterCommandHeld(t, p, b, c, "CLUSTER t USING a")
		workerPID, holderPID := 0, 0
		var oid int64
		if b.PG {
			workerPID, holderPID = fixture.LockPID(t, p.Ctx, c), fixture.LockPID(t, p.Ctx, holder)
			oid = fixture.LockOID(t, p.Ctx, "t")
		}
		tx, err := holder.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		persistenceExec(t, p, b, tx, "LOCK TABLE t IN ACCESS SHARE MODE")
		finished := make(chan struct{})
		join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
			defer close(finished)
			result, err := held.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		fixture.RelationLockPending(t, p, b, workerPID, holderPID, oid, "AccessExclusiveLock", finished)
		// Progress begins only after ordinary heap admission and eligibility.
		persistenceRun(t, p, b, observer, clusterCommandProgressIdle())
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		out := join()
		if out.Err != nil {
			t.Fatal(out.Err)
		}
		n, err := out.Result.RowsAffected()
		if err != nil || n != 0 {
			t.Fatal(n, err)
		}
		persistenceRun(t, p, b, observer, clusterCommandProgressIdle())
		persistenceRun(t, p, b, observer, clusterCommandBits(true, false))
		persistenceRun(t, p, b, observer, clusterCommandRows())
	})
}
