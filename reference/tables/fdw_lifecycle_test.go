package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWLifecycle(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		run(fixture.Select("SELECT id FROM fdw_data ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		if b.PG {
			return
		}
		before := r.snapshot()
		q := "SELECT id,label FROM fdw_data ORDER BY id"
		held := fdwHeld(t, p, b, c, q)
		finished := fdwReleased(t, r, before, 1)
		fixture.RunStep(t, p.Ctx, held, held.recorder, fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
		if after := r.snapshot(); after != finished {
			t.Fatal("public Rows.Next/Close performed foreign work", finished, after)
		}
		for _, query := range []string{"SELECT id FROM fdw_data LIMIT 1", "SELECT id FROM fdw_data"} {
			before = r.snapshot()
			if _, err := c.ExecContext(p.Ctx, query); err != nil {
				t.Fatal(err)
			}
			fdwReleased(t, r, before, 1)
		}
		for _, method := range []string{"GetForeignRelSize", "GetForeignPaths", "GetForeignPlan", "BeginForeignScan", "IterateForeignScan", "EndForeignScan", "ReScanForeignScan"} {
			t.Run(method, func(t *testing.T) {
				failure := errors.New("actual SQL reader injected " + method)
				r.setHook(func(ctx context.Context, at string) error {
					if at == method {
						return failure
					}
					return ctx.Err()
				})
				defer r.setHook(nil)
				before := r.snapshot()
				query := "SELECT id,label FROM fdw_data"
				if method == "ReScanForeignScan" {
					query = "SELECT k.column1,(SELECT id FROM fdw_data WHERE id=k.column1 OFFSET 0) AS id FROM (VALUES(1),(2),(1)) k"
				}
				fdwHostQueryError(t, p.Ctx, c, query, failure)
				owned := 1
				if method == "GetForeignRelSize" || method == "GetForeignPaths" || method == "GetForeignPlan" {
					owned = 0
				}
				fdwReleased(t, r, before, owned)
			})
		}
		primary, secondary := errors.New("reader read failed"), errors.New("reader cleanup failed")
		r.setHook(func(ctx context.Context, method string) error {
			switch method {
			case "IterateForeignScan":
				return primary
			case "EndForeignScan":
				if ctx.Err() != nil {
					return errors.New("cleanup inherited canceled context")
				}
				return secondary
			}
			return ctx.Err()
		})
		before = r.snapshot()
		fdwHostQueryError(t, p.Ctx, c, "SELECT id FROM fdw_data", primary)
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		before = r.snapshot()
		run(fixture.ErrorDiagnostic("SELECT 10/(id-2) FROM fdw_data", fdwDiagnostic("22012", "division by zero")))
		fdwReleased(t, r, before, 1)

		ctx, cancel := context.WithCancel(p.Ctx)
		entered, done := make(chan struct{}), make(chan error, 1)
		r.setHook(func(ctx context.Context, method string) error {
			if method == "IterateForeignScan" {
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			}
			if method == "EndForeignScan" && ctx.Err() != nil {
				return errors.New("canceled cleanup context")
			}
			return ctx.Err()
		})
		defer r.setHook(nil)
		before = r.snapshot()
		go func() {
			rows, err := c.QueryContext(ctx, "SELECT id FROM fdw_data")
			if rows != nil {
				rows.Close()
				if err == nil {
					err = errors.New("canceled eager query published rows")
				}
			}
			done <- err
		}()
		joined := false
		defer func() {
			cancel()
			if !joined {
				<-done
			}
		}()
		select {
		case <-entered:
		case err := <-done:
			joined = true
			t.Fatal("query completed before actual scan cancellation admission", err)
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		cancel()
		err := <-done
		joined = true
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fdwReleased(t, r, before, 1)
	})
}
