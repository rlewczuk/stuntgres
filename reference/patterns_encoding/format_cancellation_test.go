package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceFormatCancellation(t *testing.T) {
	formatReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT format('%s',$1::text) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		if b.PG {
			// Server-observed admission/cancel/join, not a fast context race.
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancel(t, p.Ctx, c, prepared, true)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"same owner"}}, "same owner"))
			}
			return
		}
		old, err := c.QueryContext(p.Ctx, q, "retained")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		for _, expired := range []bool{false, true} {
			var ctx context.Context
			var cancel context.CancelFunc
			want := error(context.Canceled)
			if expired {
				ctx, cancel = context.WithDeadline(p.Ctx, time.Now().Add(-time.Second))
				want = context.DeadlineExceeded
			} else {
				ctx, cancel = context.WithCancel(p.Ctx)
				cancel()
			}
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = stmt.QueryContext(ctx, strings.Repeat("é", 65536))
				} else {
					rows, err = c.QueryContext(ctx, q, strings.Repeat("é", 65536))
				}
				if rows != nil {
					rows.Close()
					t.Fatal("canceled native format published cursor")
				}
				var state interface{ SQLState() string }
				if !errors.Is(err, want) || errors.As(err, &state) {
					t.Fatalf("ordinary cancellation %v, want %v without SQLSTATE", err, want)
				}
			}
			cancel()
		}
		fixture.StringCoreRetainedRows(t, old, nil, []string{"v"}, []string{"TEXT"}, [][]any{{"retained"}}, false)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"same owner"}}, "same owner"))
	})
}
