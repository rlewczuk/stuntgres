package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDblinkGetPkeyCancellation(t *testing.T) {
	for _, route := range []string{"direct", "held"} {
		t.Run(route, func(t *testing.T) {
			pkeyCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{"CREATE TABLE pkey_cancel(a integer,b integer,PRIMARY KEY(b,a))", "CREATE TABLE pkey_cancel_empty(a integer)"} {
					run(fixture.Exec(q, 0))
				}
				var x fixture.Executor = c
				if route == "held" {
					x = pkeyHeld(t, p, c, pkeyCall)
				}
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := x.QueryContext(ctx, pkeyCall, "pkey_cancel")
					if rows != nil {
						rows.Close()
						t.Fatal("precancel exposed rows")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
				}
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("BEGIN", 0))
				defer peer.ExecContext(context.Background(), "ROLLBACK")
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("LOCK TABLE pkey_cancel IN ACCESS EXCLUSIVE MODE", 0))
				fixture.RelationLockActiveCancel(t, p, b, c, pkeyCall, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := x.QueryContext(ctx, pkeyCall, "pkey_cancel")
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
				for _, s := range []fixture.Step{
					pkeyRows([][]any{{int64(1), "b"}, {int64(2), "a"}}, "pkey_cancel"),
					pkeyRows(nil, "pkey_cancel_empty"),
					pkeyError(pkeyCall, "42P01", `relation "missing_pkey" does not exist`, "missing_pkey"),
					pkeyRows(nil, nil),
				} {
					fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("BEGIN", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("LOCK TABLE pkey_cancel,pkey_cancel_empty IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("ROLLBACK", 0))
				}
			})
		})
	}
}
