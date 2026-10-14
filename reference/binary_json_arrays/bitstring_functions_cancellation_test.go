package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceBitStringFunctionsCancellation(t *testing.T) {
	bitStringFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE bsf_cancel(v VARBIT)", 0))
		run(fixture.Exec("INSERT INTO bsf_cancel VALUES(B'10110')", 1))
		q := "SELECT bit_count(v) AS c,substring(v FROM $1 FOR $2) AS s FROM bsf_cancel"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		wq := "UPDATE bsf_cancel SET v=overlay(v PLACING $1 FROM 2 FOR 2)"
		write, err := c.PrepareContext(p.Ctx, wq)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			expired, expire := context.WithDeadline(p.Ctx, time.Unix(0, 0))
			defer expire()
			for _, tc := range []struct {
				ctx  context.Context
				want error
			}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
				for _, held := range []bool{false, true} {
					var rows *sql.Rows
					var err error
					if held {
						rows, err = stmt.QueryContext(tc.ctx, 2, 2)
					} else {
						rows, err = c.QueryContext(tc.ctx, q, 2, 2)
					}
					if rows != nil {
						rows.Close()
						t.Fatal("canceled query published cursor")
					}
					if !errors.Is(err, tc.want) {
						t.Fatal(err)
					}
					var result sql.Result
					if held {
						result, err = write.ExecContext(tc.ctx, "00")
					} else {
						result, err = c.ExecContext(tc.ctx, wq, "00")
					}
					if result != nil || !errors.Is(err, tc.want) {
						t.Fatal("canceled write", result, err)
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"c", "s"}, []string{"INT8", "BIT"}, [][]any{{int64(3), "01"}}, 2, 2))
				}
			}
		}
		// pq fast precancel output/transport behavior is intentionally not asserted.
		// Both backends prove the same retained physical owner and feature statements.
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: wq}, b.Recorder, fixture.Exec(wq, 1, "00"))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"c", "s"}, []string{"INT8", "BIT"}, [][]any{{int64(2), "00"}}, 2, 2))
	})
}
