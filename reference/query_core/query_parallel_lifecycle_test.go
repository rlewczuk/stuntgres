package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryParallelCancellation(t *testing.T) {
	queryParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		queryParallelSeed(t, p, b, c)
		queryParallelWorkers(t, p, b, c, "2")
		read := "SELECT id FROM parallel_rows WHERE id=$1 ORDER BY id"
		held, err := c.PrepareContext(p.Ctx, read)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		recover := func() {
			rows, err := held.QueryContext(p.Ctx, int64(4))
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarRows(t, b.PG, rows, []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}, false)
		}
		q := "SELECT pg_sleep(10) FROM parallel_rows"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, prepared := range []bool{false, true} {
			var x fixture.Executor = c
			if prepared {
				x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			fixture.SleepActiveCancel(t, p, b, c, q, nil, func(ctx context.Context) fixture.RelationOutcome {
				rows, err := x.QueryContext(ctx, q)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			recover()
			queryParallelRun(t, p, b, c, fixture.Exec("SET statement_timeout='1s'", 0))
			queryParallelFailure(t, p.Ctx, b, x, q, queryParallelDiagnostic("57014", "canceling statement due to statement timeout"))
			queryParallelRun(t, p, b, c, fixture.Exec("RESET statement_timeout", 0))
			recover()
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = held.QueryContext(ctx, int64(1))
				} else {
					rows, err = c.QueryContext(ctx, read, int64(1))
				}
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled query published rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
			recover()
		}
	})
}

func TestReferenceQueryParallelLifecycle(t *testing.T) {
	queryParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		queryParallelSeed(t, p, b, c)
		queryParallelWorkers(t, p, b, c, "2")
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, q := range []string{"SELECT id,k,p FROM parallel_rows ORDER BY id", "SELECT id,k+1 AS k,p FROM parallel_rows ORDER BY id"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for _, prepared := range []bool{false, true} {
				queryParallelRun(t, p, b, peer, fixture.Exec("TRUNCATE parallel_rows", 0))
				queryParallelRun(t, p, b, peer, fixture.Exec("INSERT INTO parallel_rows VALUES(1,2,'a'),(2,NULL,'b'),(3,1,'c'),(4,2,'a'),(5,NULL,'b'),(6,1,NULL)", 6))
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, q)
				}
				if err != nil {
					stmt.Close()
					t.Fatal(err)
				}
				defer rows.Close()
				queryParallelRun(t, p, b, peer, fixture.Exec("UPDATE parallel_rows SET p='changed' WHERE id=1", 1))
				queryParallelRun(t, p, b, peer, fixture.Exec("INSERT INTO parallel_rows VALUES(7,9,'new')", 1))
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
						t.Fatal(err)
					}
				}
				first, second := int64(2), int64(1)
				if strings.Contains(q, "k+1") {
					first, second = 3, 2
				}
				fixture.ScalarRows(t, b.PG, rows, []string{"id", "k", "p"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), first, "a"}, {int64(2), nil, "b"}, {int64(3), second, "c"}, {int64(4), first, "a"}, {int64(5), nil, "b"}, {int64(6), second, nil}}, false)
				queryParallelRun(t, p, b, peer, fixture.Select("SELECT id,p FROM parallel_rows WHERE id=1 OR id=7 ORDER BY id", []string{"id", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "changed"}, {int64(7), "new"}}))
			}
			stmt.Close()
		}
	})
}

func TestReferenceQueryParallelFeatureSmoke(t *testing.T) {
	queryParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		queryParallelSeed(t, p, b, c)
		q := "SELECT id FROM parallel_rows WHERE k=$1 ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, workers := range []string{"0", "2"} {
			queryParallelWorkers(t, p, b, c, workers)
			queryParallelRun(t, p, b, c, queryParallelBag("SELECT k,p FROM parallel_rows", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "a"}, {nil, "b"}, {int64(1), "c"}, {int64(2), "a"}, {nil, "b"}, {int64(1), nil}}))
			queryParallelRun(t, p, b, c, fixture.Select("SELECT id FROM parallel_rows ORDER BY k DESC NULLS LAST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(4)}, {int64(3)}, {int64(6)}, {int64(2)}, {int64(5)}}))
			for _, tc := range []struct {
				arg  any
				want [][]any
			}{{int64(2), [][]any{{int64(1)}, {int64(4)}}}, {int64(1), [][]any{{int64(3)}, {int64(6)}}}, {nil, nil}} {
				rows, err := stmt.QueryContext(p.Ctx, tc.arg)
				if err != nil {
					t.Fatal(err)
				}
				fixture.ScalarRows(t, b.PG, rows, []string{"id"}, []string{"INT4"}, tc.want, false)
			}
			queryParallelFailure(t, p.Ctx, b, c, "SELECT 10/(id-6) FROM parallel_rows", queryParallelDiagnostic("22012", "division by zero"))
		}
		queryParallelRun(t, p, b, c, fixture.Exec("BEGIN", 0))
		queryParallelRun(t, p, b, c, fixture.Exec("SET LOCAL max_parallel_workers_per_gather=0", 0))
		queryParallelRun(t, p, b, c, fixture.Select("SHOW max_parallel_workers_per_gather", []string{"max_parallel_workers_per_gather"}, []string{"TEXT"}, [][]any{{"0"}}))
		queryParallelRun(t, p, b, c, fixture.Exec("COMMIT", 0))
		queryParallelRun(t, p, b, c, fixture.Select("SHOW max_parallel_workers_per_gather", []string{"max_parallel_workers_per_gather"}, []string{"TEXT"}, [][]any{{"2"}}))
		queryParallelRun(t, p, b, c, fixture.Exec("RESET max_parallel_workers_per_gather", 0))
		queryParallelRun(t, p, b, c, fixture.Select("SHOW max_parallel_workers_per_gather", []string{"max_parallel_workers_per_gather"}, []string{"TEXT"}, [][]any{{"2"}}))
	})
}
