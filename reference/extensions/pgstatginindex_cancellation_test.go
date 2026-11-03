package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatginindexCancellation(t *testing.T) {
	for _, mode := range []string{"direct", "held"} {
		t.Run(mode, func(t *testing.T) {
			pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				pgstatginindexSeed(t, p, b, c)
				var oid int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'gg_part.gg_array'::regclass::oid").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				q := "SELECT * FROM gg_part.pgstatginindex($1::oid)"
				query := func(ctx context.Context) (*sql.Rows, error) { return c.QueryContext(ctx, q, oid) }
				if mode == "held" {
					st, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer st.Close()
					query = func(ctx context.Context) (*sql.Rows, error) { return st.QueryContext(ctx, oid) }
				}
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					rows, err := query(ctx)
					if rows != nil {
						rows.Close()
						t.Fatal("pre-canceled query published rows")
					}
					fixture.LexicalOrdinary(t, err, context.Canceled)
				}
				holder, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
				waiterPID, holderPID := 0, 0
				if b.PG {
					waiterPID = fixture.LockPID(t, p.Ctx, c)
					holderPID = fixture.LockPID(t, p.Ctx, holder)
				}
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, holder, b.Recorder, s) }
				run(fixture.Exec("BEGIN", 0))
				defer holder.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DROP INDEX gg_part.gg_array", 0))
				fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
					if b.PG {
						fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "AccessShareLock")
					}
				}, func(ctx context.Context) fixture.RelationOutcome {
					rows, err := query(ctx)
					return fixture.RelationOutcome{Rows: rows, Err: err}
				})
				run(fixture.Exec("ROLLBACK", 0))
				pgstatginindexBoth(t, p, b, c, pgstatginindexStats(q, oid))
				// Exclusive reacquisition, not just another compatible read, proves
				// canceled admission released its exact-index lock contribution.
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SET LOCAL lock_timeout='1s'", 0))
				run(fixture.Exec("DROP INDEX gg_part.gg_array", 0))
				run(fixture.Exec("ROLLBACK", 0))
				pgstatginindexBoth(t, p, b, c, pgstatginindexStats(q, oid))
				pgstatginindexContents(t, p, b, c, []int64{1, 2}, []int64{1})
			})
		})
	}
}
