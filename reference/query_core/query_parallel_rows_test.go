package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryParallelBags(t *testing.T) {
	queryParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		queryParallelSeed(t, p, b, c)
		bag := [][]any{{int64(2), "a"}, {nil, "b"}, {int64(1), "c"}, {int64(2), "a"}, {nil, "b"}, {int64(1), nil}}
		query := "SELECT k,p FROM parallel_rows"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, workers := range []string{"0", "1", "2", "7"} {
			queryParallelWorkers(t, p, b, c, workers)
			queryParallelRun(t, p, b, c, queryParallelBag(query, []string{"k", "p"}, []string{"INT4", "TEXT"}, bag))
			rows, err := stmt.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarRows(t, b.PG, rows, []string{"k", "p"}, []string{"INT4", "TEXT"}, bag, true)
			queryParallelRun(t, p, b, c, queryParallelBag("SELECT k,p FROM parallel_rows WHERE k>=2", []string{"k", "p"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "a"}, {int64(2), "a"}}))
			for _, predicate := range []string{"k=NULL", "FALSE", "NULL"} {
				queryParallelRun(t, p, b, c, queryParallelBag("SELECT 10/(id-id) AS v FROM parallel_rows WHERE "+predicate, []string{"v"}, []string{"INT4"}, nil))
			}
		}
		queryParallelRun(t, p, b, c, fixture.Exec("CREATE TABLE parallel_boundary(id INT NOT NULL,k INT,p TEXT)", 0))
		query = "SELECT id,k,p FROM parallel_boundary"
		stmt2, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt2.Close()
		for _, n := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 13, 17} {
			queryParallelRun(t, p, b, c, fixture.Exec("TRUNCATE parallel_boundary", 0))
			want := make([][]any, 0, n)
			projected := make([][]any, 0, n)
			values := make([]string, n)
			for i := 1; i <= n; i++ {
				k, value := any(int64(i%3)), "0"
				if i%3 == 0 {
					k, value = nil, "NULL"
				} else {
					value = fmt.Sprint(i % 3)
				}
				text, literal := any("duplicate"), "'duplicate'"
				if i%4 == 0 {
					text, literal = nil, "NULL"
				}
				values[i-1] = fmt.Sprintf("(%d,%s,%s)", i, value, literal)
				want = append(want, []any{int64(i), k, text})
				projected = append(projected, []any{k, text})
			}
			if n != 0 {
				queryParallelRun(t, p, b, c, fixture.Exec("INSERT INTO parallel_boundary VALUES"+strings.Join(values, ","), int64(n)))
			}
			for _, workers := range []string{"0", "1", "2", "7"} {
				queryParallelWorkers(t, p, b, c, workers)
				queryParallelRun(t, p, b, c, queryParallelBag(query, []string{"id", "k", "p"}, []string{"INT4", "INT4", "TEXT"}, want))
				rows, err := stmt2.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				fixture.ScalarRows(t, b.PG, rows, []string{"id", "k", "p"}, []string{"INT4", "INT4", "TEXT"}, want, true)
				queryParallelRun(t, p, b, c, queryParallelBag("SELECT k,p FROM parallel_boundary", []string{"k", "p"}, []string{"INT4", "TEXT"}, projected))
				zero := make([][]any, n)
				for i := range zero {
					zero[i] = []any{}
				}
				queryParallelRun(t, p, b, c, queryParallelBag("SELECT FROM parallel_boundary", []string{}, []string{}, zero))
			}
		}
	})
}

func TestReferenceQueryParallelMergeOrder(t *testing.T) {
	queryParallelScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		queryParallelSeed(t, p, b, c)
		type ordering struct {
			clause string
			ids    []int64
		}
		for _, order := range []ordering{
			{"k DESC NULLS LAST,id ASC", []int64{1, 4, 3, 6, 2, 5}},
			{"k ASC NULLS FIRST,id ASC", []int64{2, 5, 3, 6, 1, 4}},
			{"k+1 DESC NULLS LAST,id ASC", []int64{1, 4, 3, 6, 2, 5}},
			{"k DESC NULLS LAST,id DESC", []int64{4, 1, 6, 3, 5, 2}},
		} {
			q := "SELECT id FROM parallel_rows ORDER BY " + order.clause
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			want := make([][]any, len(order.ids))
			for i, id := range order.ids {
				want[i] = []any{id}
			}
			for _, workers := range []string{"0", "1", "2", "7"} {
				queryParallelWorkers(t, p, b, c, workers)
				queryParallelRun(t, p, b, c, fixture.Select(q, []string{"id"}, []string{"INT4"}, want))
				rows, err := stmt.QueryContext(p.Ctx)
				if err != nil {
					stmt.Close()
					t.Fatal(err)
				}
				fixture.ScalarRows(t, b.PG, rows, []string{"id"}, []string{"INT4"}, want, false)
			}
			stmt.Close()
		}
		// Peer ordering is not specified; preserve multiplicities instead.
		queryParallelRun(t, p, b, c, queryParallelBag("SELECT k FROM parallel_rows ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {int64(2)}, {int64(2)}, {nil}, {nil}}))
		q := "SELECT id FROM parallel_rows WHERE k=$1 ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, workers := range []string{"0", "2", "7", "1"} {
			queryParallelWorkers(t, p, b, c, workers)
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
		}
	})
}
