package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsMCVItemsLifecycle(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		mcvSetup(t, s)
		held, err := s.conn.PrepareContext(s.pair.Ctx, mcvItemsQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func(want ...mcvItemsExpected) {
			t.Helper()
			rows, err := held.QueryContext(s.pair.Ctx, "mcv_s")
			if err != nil {
				t.Fatal(err)
			}
			mcvItemsAssert(t, mcvItemsConsume(t, rows, s.pg), want...)
		}
		check(mcvItemsFour...)
		partial, err := held.QueryContext(s.pair.Ctx, "mcv_s")
		if err != nil {
			t.Fatal(err)
		}
		if !partial.Next() {
			partial.Close()
			t.Fatal("missing first demanded row", partial.Err())
		}
		if err := partial.Close(); err != nil {
			t.Fatal(err)
		}
		check(mcvItemsFour...) // Closing early must not consume a shared cursor.
		old, err := held.QueryContext(s.pair.Ctx, "mcv_s")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if s.pg {
			mcvItemsAssert(t, mcvItemsConsume(t, old, true), mcvItemsFour...)
		}
		s.run(fixture.Exec("INSERT INTO mcv_t VALUES(2,2)", 1))
		check(mcvItemsFour...)
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		check(mcvItemsFive...)
		if !s.pg {
			mcvItemsAssert(t, mcvItemsConsume(t, old, false), mcvItemsFour...)
		}
		old, err = held.QueryContext(s.pair.Ctx, "mcv_s")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if s.pg {
			mcvItemsAssert(t, mcvItemsConsume(t, old, true), mcvItemsFive...)
		}
		s.run(fixture.Exec("DROP STATISTICS mcv_s", 0))
		check()
		if !s.pg {
			mcvItemsAssert(t, mcvItemsConsume(t, old, false), mcvItemsFive...)
		}
		s.run(fixture.Exec("CREATE STATISTICS mcv_s(mcv) ON a,b FROM mcv_t", 0))
		check()
		s.run(fixture.Exec("ANALYZE mcv_t", 0))
		check(mcvItemsFive...)
		if !s.pg {
			ctx, cancel := context.WithCancel(s.pair.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				if prepared {
					rows, err = held.QueryContext(ctx, "mcv_s")
				} else {
					rows, err = s.conn.QueryContext(ctx, mcvItemsQuery, "mcv_s")
				}
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled inspection exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("pre-canceled inspection", err)
				}
			}
			check(mcvItemsFive...)
		}
	})
	t.Run("admitted-query-cancellation", mcvItemsAdmittedCancellation)
}

func mcvItemsAdmittedCancellation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					admin, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer admin.Close()
					run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, step) }
					s := ndistinctScenario{run: run, conn: admin, pair: p, pg: b.PG}
					mcvSetup(t, s)
					run(fixture.Exec("CREATE TABLE mcv_items_gate(v INT)", 0))
					run(fixture.Exec("INSERT INTO mcv_items_gate VALUES(1)", 1))
					worker, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer worker.Close()
					query := "SELECT m.* FROM mcv_items_gate g CROSS JOIN LATERAL pg_mcv_list_items(" + mcvItemsArgument + ") m WHERE g.v=1 ORDER BY m.index"
					var held *sql.Stmt
					if prepared {
						held, err = worker.PrepareContext(p.Ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					holder, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Close()
					defer holder.ExecContext(context.Background(), "ROLLBACK")
					waiterPID, holderPID := 0, 0
					var oid int64
					if b.PG {
						waiterPID = fixture.LockPID(t, p.Ctx, worker)
						holderPID = fixture.LockPID(t, p.Ctx, holder)
						oid = fixture.LockOID(t, p.Ctx, "mcv_items_gate")
					}
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("BEGIN", 0))
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE mcv_items_gate IN ACCESS EXCLUSIVE MODE", 0))
					fixture.BinaryConversionActiveCancelAfter(t, p, b, worker, query, "(*relationRequest).wait", func(ctx context.Context) {
						if b.PG {
							fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, oid, "AccessShareLock")
						}
					}, func(ctx context.Context) fixture.RelationOutcome {
						var rows *sql.Rows
						var err error
						if prepared {
							rows, err = held.QueryContext(ctx, "mcv_s")
						} else {
							rows, err = worker.QueryContext(ctx, query, "mcv_s")
						}
						return fixture.RelationOutcome{Rows: rows, Err: err}
					})
					fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("ROLLBACK", 0))
					mcvItemsCheck(t, s, "mcv_s", mcvItemsFour...)
				})
			}
		})
	}
}
