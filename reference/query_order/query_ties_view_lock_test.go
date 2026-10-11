package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceQueryTiesViews(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		keys        []int64
	}{
		{"ties_view", "SELECT k FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", []int64{1, 2, 2}},
		{"ties_values_view", "VALUES(1),(2),(2),(3) ORDER BY 1 FETCH FIRST 2 ROWS WITH TIES", []int64{1, 2, 2}},
		{"ties_set_view", "SELECT k FROM ties_t UNION ALL SELECT k FROM ties_t ORDER BY k FETCH FIRST 3 ROWS WITH TIES", []int64{1, 1, 2, 2, 2, 2}},
		{"ties_null_view", "SELECT k FROM ties_t ORDER BY k FETCH FIRST (NULL+1) ROWS WITH TIES", []int64{1, 2, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				referenceTiesSetup(t, p, b)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW "+tc.name+"(k) AS "+tc.query, 0))
				referenceTiesBoth(t, p, b, fixture.Select("SELECT k FROM "+tc.name+" ORDER BY k", []string{"k"}, []string{"INT4"}, referenceLimitIDs(tc.keys...)))
				// ORDER ordinals, alias spelling and whitespace are presentation
				// choices. The exported SQL must preserve executable peer semantics.
				for _, prepared := range []bool{false, true} {
					outcome := fixture.RowQuery(p.Ctx, b.DB, "SELECT pg_get_viewdef('"+tc.name+"'::regclass)", prepared)
					if outcome.Stmt != nil {
						defer outcome.Stmt.Close()
					}
					if outcome.Err != nil {
						t.Fatal(outcome.Err)
					}
					if !outcome.Rows.Next() {
						outcome.Rows.Close()
						t.Fatal("missing view definition", outcome.Rows.Err())
					}
					var definition string
					if err := outcome.Rows.Scan(&definition); err != nil {
						outcome.Rows.Close()
						t.Fatal(err)
					}
					if outcome.Rows.Next() || outcome.Rows.Err() != nil {
						outcome.Rows.Close()
						t.Fatal("extra definition", outcome.Rows.Err())
					}
					if err := outcome.Rows.Close(); err != nil {
						t.Fatal(err)
					}
					replay := "ties_replay_" + tc.name
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW "+replay+" AS "+definition, 0))
					referenceTiesBoth(t, p, b, fixture.Select("SELECT k FROM "+replay+" ORDER BY k", []string{"k"}, []string{"INT4"}, referenceLimitIDs(tc.keys...)))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP VIEW "+replay, 0))
				}
			})
		})
	}
	t.Run("nested hidden key rename", func(t *testing.T) {
		referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
			referenceTiesSetup(t, p, b)
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE VIEW ties_hidden AS SELECT id FROM ties_t ORDER BY k FETCH FIRST 2 ROWS WITH TIES", 0),
				fixture.Exec("CREATE VIEW ties_nested AS SELECT id FROM ties_hidden ORDER BY id", 0),
				fixture.Exec("ALTER TABLE ties_t RENAME COLUMN k TO renamed_k", 0),
				fixture.Exec("ALTER TABLE ties_t RENAME TO ties_renamed", 0),
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			referenceTiesBoth(t, p, b, fixture.Select("SELECT id FROM ties_nested ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3)))
		})
	})
}

func TestReferenceQueryTiesLocks(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, suffix string
			keys         []int64
			held         []int
			free         []int
		}{
			{"boundary", "FETCH FIRST 2 ROWS WITH TIES FOR UPDATE", []int64{1, 2, 2}, []int{1, 2, 3, 4}, []int{5}},
			{"offset", "OFFSET 1 FETCH FIRST ROW WITH TIES FOR UPDATE NOWAIT", []int64{2, 2}, []int{1, 2, 3, 4}, []int{5}},
			{"zero", "FETCH FIRST 0 ROWS WITH TIES FOR UPDATE", nil, nil, []int{1, 2, 3, 4, 5}},
		} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				referenceTiesScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
					defer cancel()
					a, other := fixture.LockConnections(t, ctx, b.DB)
					fixture.LockExec(t, ctx, a, "CREATE TABLE ties_t(id INT,k INT)", 0)
					fixture.LockExec(t, ctx, a, "INSERT INTO ties_t VALUES(1,1),(2,2),(3,2),(4,3),(5,4)", 5)
					holder, err := a.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					fixture.ViewValues(t, fixture.RowQuery(ctx, holder, "SELECT k FROM ties_t ORDER BY k "+tc.suffix, prepared), "k", "INT4", tc.keys...)
					for _, id := range tc.free {
						result, err := other.ExecContext(ctx, "UPDATE ties_t SET k=k WHERE id=$1", id)
						if err != nil {
							t.Fatal("free tail update", id, err)
						}
						fixture.LockAffected(t, result, 1)
					}
					// Positively observe each held occurrence, including the first
					// nonreturned lookahead, using existing real-server wait evidence.
					// Native exact queue ownership is supplemented by package units.
					if b.PG && len(tc.held) > 0 {
						for _, id := range tc.held {
							// lib/pq cancellation may discard its transport. Each
							// blocked probe owns a fresh connection, never the free-tail witness.
							probeConn, err := b.DB.Conn(ctx)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { probeConn.Close() })
							ap, bp := fixture.RowPIDs(t, ctx, a, probeConn)
							probe, err := probeConn.BeginTx(ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							q := "UPDATE ties_t SET k=k WHERE id=" + fixture.Position(id)
							probeCtx, stop := context.WithCancel(ctx)
							done := fixture.RowAsync(t, probeCtx, probe, q, prepared, false)
							fixture.RowWait(t, ctx, bp, ap)
							stop()
							out := fixture.LockResult(t, done)
							if out.Err == nil || out.Result != nil {
								t.Fatal("canceled blocked update published result", out.Err)
							}
							probe.Rollback()
							probeConn.Close()
						}
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					for _, id := range tc.held {
						result, err := other.ExecContext(ctx, "UPDATE ties_t SET k=k WHERE id=$1", id)
						if err != nil {
							t.Fatal("released hold", id, err)
						}
						fixture.LockAffected(t, result, 1)
					}
				})
			})
		}
	}
}
