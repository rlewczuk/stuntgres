package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwWriteFinalFence(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	// Replacement before any Commit genuinely rolls back staged remote work.
	staleEnds := 0
	r.writer.writeHook = func(ctx context.Context, name string) error {
		if name == "EndForeignModify" {
			staleEnds++
			return stuntgres.Reset(ctx, p.Native)
		}
		return nil
	}
	fdwWriteFailedExec(t, p.Ctx, c, "INSERT INTO fdw_data VALUES(58,'stale-before-commit',true)", nil)
	r.writer.writeHook = nil
	if staleEnds != 1 {
		t.Fatal("stale modification cleanup repeated", staleEnds)
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=58", nil))
	fdwInstallLive(b, run)
	run(fixture.Exec("CREATE TABLE fdw_local_commit(id integer)", 0))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO fdw_data VALUES(56,'remote-before-fence',true)", 1))
	run(fixture.Exec("INSERT INTO fdw_local_commit VALUES(57)", 1))
	calls := 0
	r.writer.writeHook = func(ctx context.Context, name string) error {
		if name == "Commit" {
			calls++
			return stuntgres.Reset(ctx, p.Native)
		}
		return nil
	}
	fdwWriteFailedExec(t, p.Ctx, c, "COMMIT AND CHAIN", nil)
	r.writer.writeHook = nil
	if calls != 1 {
		t.Fatal("commit retried", calls)
	}
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=56", [][]any{{int64(56), "remote-before-fence", true}}))
	// Replacement removed the local catalog. No stale CHAIN incarnation may
	// have been installed and no callback may claim to undo the remote commit.
	run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='fdw_local_commit'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("ROLLBACK", 0))
}
