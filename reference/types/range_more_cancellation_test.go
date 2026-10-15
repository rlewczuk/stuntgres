package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeMoreCancellation(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE more_cancel(a int4range,m int4multirange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO more_cancel VALUES ('[1,4]','{[1,3),[8,9)}')", 1))
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil { t.Fatal(err) }
		defer conn.Close()
		for _, tc := range []struct { query, typ string; rows [][]any }{
			{"SELECT multirange(a) AS v FROM more_cancel", "INT4MULTIRANGE", [][]any{{"{[1,5)}"}}},
			{"SELECT unnest(m) AS v FROM more_cancel", "INT4RANGE", [][]any{{"[1,3)"}, {"[8,9)"}}},
		} {
			stmt, err := conn.PrepareContext(p.Ctx, tc.query)
			if err != nil { t.Fatal(err) }
			func() {
				defer stmt.Close()
				// Native precancellation is a Go boundary, not an invented pq
				// transport/server diagnostic. Ordinary SQL phases are shared.
				if !b.PG {
					ctx, cancel := context.WithCancel(p.Ctx); cancel()
					for _, query := range []func() (*sql.Rows, error){
						func() (*sql.Rows, error) { return conn.QueryContext(ctx, tc.query) },
						func() (*sql.Rows, error) { return stmt.QueryContext(ctx) },
					} {
						rows, err := query()
						var state interface { SQLState() string }
						if rows != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) { if rows != nil { rows.Close() }; t.Fatal("cancellation crossed SQL boundary", err) }
					}
				}
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.query}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, rangeSelect(b, tc.query, []string{"v"}, []string{tc.typ}, tc.rows, nil))
			}()
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, rangeSelect(b, "SELECT a,m FROM more_cancel", []string{"a", "m"}, []string{"INT4RANGE", "INT4MULTIRANGE"}, [][]any{{"[1,5)", "{[1,3),[8,9)}"}}, []bool{true, true}))
	})
}
