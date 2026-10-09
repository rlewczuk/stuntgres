package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"testing"
	"time"
)

func queryLockMigratedDeadline(t *testing.T, err error, pg bool) bool {
	if !pg {
		return errors.Is(err, context.DeadlineExceeded)
	}
	referenceState(t, err, "57014")
	return true
}
func TestReferenceQueryLimitMigratedLockSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {

			db := backend.db
			var err error

			for _, q := range []string{"CREATE TABLE t(id INT NOT NULL,v INT)", "INSERT INTO t VALUES(1,10),(2,20),(3,30),(4,40)"} {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			for _, q := range []string{
				"CREATE TABLE u(id INT)", "INSERT INTO u VALUES(1),(2),(3),(4)",
				"CREATE VIEW marked AS SELECT id,v FROM t FOR UPDATE",
				"CREATE VIEW ordered_marked AS SELECT id,v FROM t ORDER BY id FOR UPDATE",
				"CREATE VIEW limited_marked AS SELECT id,v FROM t ORDER BY id LIMIT 1 FOR UPDATE",
				"CREATE VIEW offset_marked AS SELECT id,v FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE",
				"CREATE VIEW zero_marked AS SELECT id,v FROM t LIMIT 0 FOR UPDATE",
			} {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(q, err)
				}
			}
			ctx, cancel := context.WithTimeout(p.ctx, 15*time.Second)
			defer cancel()
			for _, q := range []string{"SELECT id FROM t ORDER BY id OFFSET 1 LIMIT 1 FOR UPDATE", "SELECT id FROM t ORDER BY id LIMIT 0 FOR UPDATE"} {
				holder, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				queryLockRollbackCleanup(t, holder)
				defer holder.Rollback()
				rows, err := holder.QueryContext(ctx, q)
				want := [][]any{}
				if q != "SELECT id FROM t ORDER BY id LIMIT 0 FOR UPDATE" {
					want = [][]any{{int64(2)}}
				}
				queryLockMigratedRows(t, rows, err, []string{"id"}, []string{"INT4"}, want)
				for _, id := range []int{3, 4} {
					if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=$1", id); err != nil {
						holder.Rollback()
						t.Fatal("tail write blocked", id, err)
					}
				}
				if len(want) > 0 {
					blocked, stop := context.WithTimeout(ctx, 250*time.Millisecond)
					_, err := db.ExecContext(blocked, "UPDATE t SET v=v+1 WHERE id=1")
					stop()
					if !queryLockMigratedDeadline(t, err, backend.pg) {
						holder.Rollback()
						t.Fatal("skipped-row writer was not cancelled", err)
					}
				} else {
					for _, id := range []int{1, 2} {
						if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=$1", id); err != nil {
							holder.Rollback()
							t.Fatal("zero held a row", id, err)
						}
					}
				}
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=1"); err != nil {
					t.Fatal("cancelled waiter left a grant", err)
				}
			}
			for _, tc := range []struct {
				q, name, typ string
				want         [][]any
				free         []int
			}{
				{"SELECT t.id FROM t JOIN u ON t.id=u.id ORDER BY t.id OFFSET 1 LIMIT 1 FOR UPDATE", "id", "INT4", [][]any{{int64(2)}}, []int{3, 4}},
				{"SELECT count(*) FROM marked WHERE id=1", "count", "INT8", [][]any{{int64(1)}}, []int{2, 3, 4}},
				{"SELECT id FROM ordered_marked WHERE id=1", "id", "INT4", [][]any{{int64(1)}}, []int{2, 3, 4}},
				{"SELECT id FROM limited_marked WHERE id=2", "id", "INT4", [][]any{}, []int{2, 3, 4}},
				{"SELECT id FROM offset_marked WHERE id=3", "id", "INT4", [][]any{}, []int{3, 4}},
				{"SELECT count(*) FROM zero_marked", "count", "INT8", [][]any{{int64(0)}}, []int{1, 2, 3, 4}},
				{"SELECT id FROM marked ORDER BY id LIMIT 0", "id", "INT4", [][]any{}, []int{1, 2, 3, 4}},
			} {
				holder, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				queryLockRollbackCleanup(t, holder)
				defer holder.Rollback()
				rows, err := holder.QueryContext(ctx, tc.q)
				queryLockMigratedRows(t, rows, err, []string{tc.name}, []string{tc.typ}, tc.want)
				for _, id := range tc.free {
					if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=$1", id); err != nil {
						holder.Rollback()
						t.Fatal(tc.q, "free frontier blocked", id, err)
					}
				}
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=1"); err != nil {
					t.Fatal("local frontier retained after rollback", err)
				}
			}
			holder, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			queryLockRollbackCleanup(t, holder)
			defer holder.Rollback()
			rows, err := holder.QueryContext(ctx, "SELECT id FROM marked ORDER BY id OFFSET 1 LIMIT 1")
			queryLockMigratedRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}})
			blocked, stop := context.WithTimeout(ctx, 250*time.Millisecond)
			_, err = db.ExecContext(blocked, "UPDATE t SET v=v+1 WHERE id=4")
			stop()
			if !queryLockMigratedDeadline(t, err, backend.pg) {
				holder.Rollback()
				t.Fatal("stored outer Sort tail writer was not cancelled", err)
			}
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "UPDATE t SET v=v+1 WHERE id=4"); err != nil {
				t.Fatal("stored tail cancellation cleanup", err)
			}
			for _, q := range []string{"CREATE TABLE cache_t(id INT,v INT)", "INSERT INTO cache_t VALUES(1,10),(2,20)", "CREATE SEQUENCE gate START WITH 1", "CREATE VIEW cache_marked AS SELECT id,v+1 AS val,nextval('gate') AS entered FROM cache_t FOR UPDATE"} {
				if _, err := db.ExecContext(ctx, q); err != nil {
					t.Fatal(q, err)
				}
			}
			for _, prepared := range []bool{false, true} {
				for _, suffix := range []string{"", " LIMIT 1"} {
					if _, err := db.ExecContext(ctx, "UPDATE cache_t SET v=10 WHERE id=1"); err != nil {
						t.Fatal(err)
					}
					if _, err := db.ExecContext(ctx, "ALTER SEQUENCE gate RESTART WITH 1"); err != nil {
						t.Fatal(err)
					}
					query := "SELECT val FROM cache_marked ORDER BY val" + suffix
					var stmt *sql.Stmt
					if prepared {
						stmt, err = db.PrepareContext(ctx, query)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { stmt.Close() })
					}
					holder, err := db.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					queryLockRollbackCleanup(t, holder)
					defer holder.Rollback()
					rows, err := holder.QueryContext(ctx, "SELECT id FROM cache_t WHERE id=1 FOR UPDATE")
					queryLockMigratedRows(t, rows, err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})
					if _, err := holder.ExecContext(ctx, "UPDATE cache_t SET v=30 WHERE id=1"); err != nil {
						holder.Rollback()
						t.Fatal(err)
					}
					type result struct {
						rows *sql.Rows
						err  error
					}
					done := make(chan result, 1)
					queryCtx, stopQuery := context.WithCancel(ctx)
					finished := false
					t.Cleanup(func() {
						stopQuery()
						holder.Rollback()
						if !finished {
							select {
							case abandoned := <-done:
								if abandoned.rows != nil {
									abandoned.rows.Close()
								}
							case <-time.After(5 * time.Second):
								t.Error("stored frontier query did not terminate")
							}
						}
						if stmt != nil {
							stmt.Close()
						}
					})
					go func() {
						var r *sql.Rows
						var e error
						if prepared {
							r, e = stmt.QueryContext(queryCtx)
						} else {
							r, e = db.QueryContext(queryCtx, query)
						}
						done <- result{r, e}
					}()
					// A real lower-view output proves that the old candidate was
					// captured before release. Exact wait/hold ownership is tested
					// internally; this adds no public testing hook or timed guess.
					for {
						var called bool
						if err := holder.QueryRowContext(ctx, "SELECT is_called FROM gate").Scan(&called); err != nil {
							holder.Rollback()
							t.Fatal(err)
						}
						if called {
							break
						}
						runtime.Gosched()
					}
					if err := holder.Commit(); err != nil {
						t.Fatal(err)
					}
					got := <-done
					finished = true
					want := [][]any{{int64(21)}, {int64(31)}}
					if suffix != "" {
						want = [][]any{{int64(21)}}
					}
					queryLockMigratedRows(t, got.rows, got.err, []string{"val"}, []string{"INT4"}, want)
					if stmt != nil {
						stmt.Close()
					}
				}
			}

		})
	}
}
