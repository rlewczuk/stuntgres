package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func connectbyAdmittedCancellation(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	connectbyRun(t, p, b, c, fixture.Exec("CREATE TABLE cb_cancel_target(v integer)", 0))
	connectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_cancel_target VALUES(99)", 1))
	query := connectbyQuery(3, "'cb_tree','k','p','pos','row2',0,'~'")
	for _, dml := range []bool{false, true} {
		func() {
			holder, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Close()
			if _, err := holder.ExecContext(p.Ctx, "BEGIN"); err != nil {
				t.Fatal(err)
			}
			rolledBack := false
			defer func() {
				if !rolledBack {
					holder.ExecContext(context.Background(), "ROLLBACK")
				}
			}()
			if _, err := holder.ExecContext(p.Ctx, "LOCK TABLE cb_tree IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			defer cancel()
			finished := make(chan struct{})
			// The existing native admission observer recognizes the managed
			// LockOperation worker, not arbitrary goroutines with similar frames.
			join := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				defer close(finished)
				if dml {
					result, err := c.ExecContext(ctx, "INSERT INTO cb_cancel_target SELECT level FROM cb_part.connectby('cb_tree','k','p','pos','row2',0,'~') AS t("+connectbyDefs[3]+")")
					return fixture.RelationOutcome{Result: result, Err: err}
				}
				rows, err := c.QueryContext(ctx, query)
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			joined := false
			defer func() {
				cancel()
				if !joined {
					got := join()
					if got.Rows != nil {
						got.Rows.Close()
					}
				}
			}()
			// cb_tree appears only inside connectby's relation-name string.
			// The outer SELECT/target-only INSERT cannot acquire this lock;
			// the scheduler wait therefore admits the actual nested SQL.
			fixture.NativeRelationLockWait(t, p.Ctx, finished)
			cancel()
			got := join()
			joined = true
			<-finished
			if got.Rows != nil {
				got.Rows.Close()
				t.Fatal("admitted canceled source exposed rows")
			}
			if !errors.Is(got.Err, context.Canceled) {
				t.Fatal("native admitted nested-query cancellation", dml, got.Err)
			}
			var diagnostic interface{ SQLState() string }
			if errors.As(got.Err, &diagnostic) {
				t.Fatal("ordinary cancellation became SQL diagnostic", got.Err)
			}
			if _, err := holder.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
				t.Fatal(err)
			}
			rolledBack = true
			connectbyRun(t, p, b, c, fixture.Select("SELECT v FROM cb_cancel_target ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
			connectbyRun(t, p, b, c, connectbySelect(3, query, connectbyTreeRows))
		}()
	}
}
