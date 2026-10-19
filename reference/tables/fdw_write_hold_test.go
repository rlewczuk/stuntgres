package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwWriteHeldParticipants(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	r.writer.orderedScans = true
	defer func() { r.writer.orderedScans = false; r.writer.writeHook = nil }()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE fdw_committed_hold NO SCROLL CURSOR WITH HOLD FOR SELECT id,label,flag FROM fdw_data", 0))
	begins, ends := r.writer.statementBegins, r.writer.statementEnds
	r.writer.writeHook = func(ctx context.Context, name string) error {
		if ctx.Value(fdwWriteContextKey{}) != "hold-completion" {
			return fmt.Errorf("%s received a stale HOLD completion context", name)
		}
		return nil
	}
	fixture.RunStep(t, context.WithValue(p.Ctx, fdwWriteContextKey{}, "hold-completion"), c, b.Recorder, fixture.Exec("COMMIT AND CHAIN", 0))
	r.writer.writeHook = nil
	if r.writer.statementBegins-begins != 1 || r.writer.statementEnds-ends != 1 {
		t.Fatal("HOLD precommit omitted participant statement completion", r.writer.statementBegins-begins, r.writer.statementEnds-ends)
	}
	run(fdwRows("FETCH 1 FROM fdw_committed_hold", [][]any{{int64(1), "one", true}}))
	if r.writer.statementBegins-begins != 1 || r.writer.statementEnds-ends != 1 {
		t.Fatal("promoted FETCH reopened provider")
	}
	// This new transaction's HOLD promotion must not survive its failed remote
	// commit, but a cursor promoted by the preceding committed owner must.
	run(fixture.Exec("INSERT INTO fdw_local_commit VALUES(63)", 1))
	run(fixture.Exec("DECLARE fdw_failed_hold NO SCROLL CURSOR WITH HOLD FOR SELECT id,label,flag FROM fdw_data", 0))
	failure := errors.New("HOLD provider commit failed")
	r.writer.writeHook = func(ctx context.Context, name string) error {
		if name == "Commit" {
			return failure
		}
		return nil
	}
	fdwWriteFailedExec(t, p.Ctx, c, "COMMIT", failure)
	r.writer.writeHook = nil
	run(fixture.ErrorDiagnostic("FETCH 1 FROM fdw_failed_hold", fdwDiagnostic("34000", `cursor "fdw_failed_hold" does not exist`)))
	run(fdwRows("FETCH 1 FROM fdw_committed_hold", [][]any{{int64(2), nil, false}}))
	run(fixture.Exec("CLOSE fdw_committed_hold", 0))
	run(fixture.Select("SELECT id FROM fdw_local_commit", []string{"id"}, []string{"INT4"}, nil))
	fdwWriteHeldNativeCommit(t, p, b, r)
	fdwWriteHeldFaults(t, p, b, c, r, run)
}

// Portable SQL witnesses use the same registered writer and genuine
// postgres_fdw source; native-only callback fault assertions remain separate.
func fdwWriteSharedHeld(run func(fixture.Step)) {
	run(fixture.Exec("BEGIN", 0))
	for _, name := range []string{"fdw_shared_hold_a", "fdw_shared_hold_b"} {
		run(fixture.Exec("DECLARE "+name+" NO SCROLL CURSOR WITH HOLD FOR SELECT id,label,flag FROM fdw_data WHERE id<3 ORDER BY id", 0))
	}
	run(fdwRows("FETCH 1 FROM fdw_shared_hold_b", [][]any{{int64(1), "one", true}}))
	run(fixture.Exec("COMMIT", 0))
	run(fdwRows("FETCH ALL FROM fdw_shared_hold_a", [][]any{{int64(1), "one", true}, {int64(2), nil, false}}))
	run(fdwRows("FETCH ALL FROM fdw_shared_hold_b", [][]any{{int64(2), nil, false}}))
	for _, name := range []string{"fdw_shared_hold_a", "fdw_shared_hold_b"} {
		run(fixture.Exec("CLOSE "+name, 0))
	}
}
