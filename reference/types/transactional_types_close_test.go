package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalTypesPhysicalClose(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// With no idle slots, returning this pinned connection physically closes
			// its driver connection rather than merely releasing a pool borrower.
			b.DB.SetMaxIdleConns(0)
			c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			var ownerPID, peerPID int
			if b.PG {
				ownerPID, peerPID = fixture.RowPIDs(t, p.Ctx, c, peer)
			}
			fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, c, "CREATE DOMAIN tt_close AS integer", 0)
			fixture.LockExec(t, p.Ctx, c, "CREATE TYPE tt_discard AS(v integer)", 0)
			fixture.LockExec(t, p.Ctx, c, "CREATE TABLE tt_discarded(v tt_close)", 0)
			fixture.LockExec(t, p.Ctx, c, "INSERT INTO tt_discarded VALUES(7)", 1)
			finished := make(chan struct{})
			join := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				return fixture.RowExecute(ctx, peer, "CREATE DOMAIN tt_close AS integer", false)
			})
			fixture.RelationLockPending(t, p, b, peerPID, ownerPID, 0, "", finished)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			outcome := join()
			fixture.State(t, outcome.Err, "")
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('tt_discard','_tt_discard')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='tt_discarded'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE tt_healthy(v tt_close)", 0)
			fixture.LockExec(t, p.Ctx, peer, "INSERT INTO tt_healthy VALUES(9)", 1)
		})
	}
}
