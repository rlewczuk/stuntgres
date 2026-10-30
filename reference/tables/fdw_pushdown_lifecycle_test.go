package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWPushdownLifecycle(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwPushdownLive(t, p, b, r, run)
		q := "SELECT l.k,l.v,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k ORDER BY l.k,l.v NULLS LAST,r.w"
		all := [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}, {int64(1), nil, int64(100)}, {int64(1), nil, int64(100)}}
		before := r.snapshot()
		held := fdwHeld(t, p, b, c, q)
		finished := r.snapshot()
		fixture.RunStep(t, p.Ctx, held, held.recorder, fixture.Select(q, []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, all))
		if !b.PG {
			fdwReleased(t, r, before, 1)
			if r.snapshot() != finished {
				t.Fatal("finalized public Rows performed provider I/O")
			}
		}
		for _, hold := range []bool{false, true} {
			run(fixture.Exec("BEGIN", 0))
			suffix := ""
			if hold {
				suffix = " WITH HOLD"
			}
			run(fixture.Exec("DECLARE fdw_push_cursor NO SCROLL CURSOR"+suffix+" FOR "+q, 0))
			run(fixture.Select("FETCH 1 FROM fdw_push_cursor", []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, all[:1]))
			if hold {
				run(fixture.Exec("COMMIT", 0))
			}
			run(fixture.Select("FETCH ALL FROM fdw_push_cursor", []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, all[1:]))
			run(fixture.Exec("CLOSE fdw_push_cursor", 0))
			if !hold {
				run(fixture.Exec("COMMIT", 0))
			}
		}
		before = r.snapshot()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_push_early NO SCROLL CURSOR FOR "+q, 0))
		run(fixture.Select("FETCH 1 FROM fdw_push_early", []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, all[:1]))
		run(fixture.Exec("CLOSE fdw_push_early", 0))
		run(fixture.Exec("COMMIT", 0))
		if !b.PG {
			fdwReleased(t, r, before, 1)
		}
		run(fixture.Exec("CREATE TABLE fdw_push_target(k integer,v integer)", 0))
		run(fixture.Exec("INSERT INTO fdw_push_target VALUES(99,99)", 1))
		run(fixture.Exec("CREATE SEQUENCE fdw_push_failure START 1", 0))
		insert := "INSERT INTO fdw_push_target SELECT (1/(3-nextval('fdw_push_failure')))::integer,l.v FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k RETURNING k,v"
		for _, prepared := range []bool{false, true} {
			run(fixture.Exec("ALTER SEQUENCE fdw_push_failure RESTART WITH 1", 0))
			s := fixture.ErrorDiagnostic(insert, fdwDiagnostic("22012", "division by zero"))
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT k,v FROM fdw_push_target", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
		}
		fdwPushdownCancel(t, p, b, c, r, false, false, false)
		for _, prepared := range []bool{false, true} {
			fdwPushdownCancel(t, p, b, c, r, true, prepared, false)
			run(fixture.Select("SELECT k,v FROM fdw_push_target", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
			fdwPushdownValue(run, "SELECT l.k,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k", []string{"k", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(100)}, {int64(1), int64(100)}, {int64(1), int64(100)}, {int64(1), int64(100)}}, true)
		}
		if b.PG {
			return
		}
		before = r.snapshot()
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_push_bad NO SCROLL CURSOR FOR "+q, 0))
		first := fdwHeld(t, p, b, c, "FETCH 1 FROM fdw_push_bad")
		fetchFailure := errors.New("current composite FETCH failed after one source row")
		pulled := 0
		r.setHook(func(ctx context.Context, method string) error {
			if method == "IterateForeignScan" {
				pulled++
				if pulled == 2 {
					return fetchFailure
				}
			}
			return ctx.Err()
		})
		fdwHostQueryError(t, p.Ctx, c, "FETCH 2 FROM fdw_push_bad", fetchFailure)
		r.setHook(nil)
		run(fixture.Exec("ROLLBACK", 0))
		fdwReleased(t, r, before, 1)
		fixture.RunStep(t, p.Ctx, first, first.recorder, fixture.Select(first.query, []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, all[:1]))
		for _, method := range []string{"GetForeignJoinPaths", "GetForeignUpperPaths", "BeginForeignQueryScan", "IterateForeignScan", "EndForeignScan"} {
			failure := errors.New("composite provider " + method)
			r.setHook(func(ctx context.Context, at string) error {
				if at == method {
					return failure
				}
				return ctx.Err()
			})
			query := q
			if method == "GetForeignJoinPaths" {
				query = "SELECT l.k,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k"
			}
			before = r.snapshot()
			fdwHostQueryError(t, p.Ctx, c, query, failure)
			r.setHook(nil)
			acquired := 1
			if method == "GetForeignJoinPaths" || method == "GetForeignUpperPaths" {
				acquired = 0
			}
			fdwReleased(t, r, before, acquired)
		}
		failure := errors.New("composite terminal End")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_push", 0))
		r.setHook(func(ctx context.Context, method string) error {
			if method == "EndForeignScan" {
				return failure
			}
			return ctx.Err()
		})
		before = r.snapshot()
		fdwHostQueryError(t, p.Ctx, c, "INSERT INTO fdw_push_target SELECT l.k,l.v FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k RETURNING k,v", failure)
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		run(fixture.Exec("ROLLBACK TO before_push", 0))
		run(fixture.Select("SELECT k,v FROM fdw_push_target", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
		run(fixture.Exec("ROLLBACK", 0))
		primary := errors.New("composite iteration primary")
		r.setHook(func(ctx context.Context, method string) error {
			if method == "IterateForeignScan" {
				return primary
			}
			if method == "EndForeignScan" {
				return failure
			}
			return ctx.Err()
		})
		before = r.snapshot()
		fdwHostQueryError(t, p.Ctx, c, q, primary)
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		// Failure originates in actual independent source SQL after one admitted
		// row, not in canned row production. No local INSERT prefix is published.
		iterations := 0
		r.setHook(func(ctx context.Context, method string) error {
			if method == "IterateForeignScan" {
				iterations++
				if iterations == 2 {
					_, err := r.source.ExecContext(ctx, "SELECT 1/0")
					return err
				}
			}
			return ctx.Err()
		})
		run(fixture.ErrorDiagnostic("INSERT INTO fdw_push_target SELECT l.k,l.v FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k RETURNING k,v", fdwDiagnostic("22012", "division by zero")))
		r.setHook(nil)
		run(fixture.Select("SELECT k,v FROM fdw_push_target", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(99)}}))
		r.mu.Lock()
		r.rowTransform = func(_ context.Context, row stuntgres.ForeignRow) (stuntgres.ForeignRow, error) {
			return row[:len(row)-1], nil
		}
		r.mu.Unlock()
		rows, err := c.QueryContext(p.Ctx, q)
		if rows != nil {
			rows.Close()
			t.Fatal("malformed width published rows")
		}
		fixture.TransactionOrdinary(t, err, "stuntgres: foreign scan row width does not match its plan")
		r.mu.Lock()
		r.rowTransform = nil
		r.mu.Unlock()
		r.mu.Lock()
		r.rowTransform = func(_ context.Context, row stuntgres.ForeignRow) (stuntgres.ForeignRow, error) {
			row[0] = "invalid integer"
			return row, nil
		}
		r.mu.Unlock()
		run(fixture.ErrorDiagnostic(q, fdwDiagnostic("22P02", `invalid input syntax for type integer: "invalid integer"`)))
		r.mu.Lock()
		r.rowTransform = nil
		r.mu.Unlock()
		fdwPushdownValue(run, q, []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, all, false)
		fdwPushdownSourceReScan(t, p, b, c, r, run)
	})
}

func fdwPushdownCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, insert, prepared, analyze bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	query := "SELECT l.k,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k"
	if insert {
		query = "INSERT INTO fdw_push_target SELECT l.k,l.v FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k"
	}
	var release func()
	var pid int
	if b.PG {
		lock, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lock.ExecContext(p.Ctx, "SELECT pg_advisory_lock(718283)"); err != nil {
			lock.Close()
			t.Fatal(err)
		}
		release = func() {
			_, _ = lock.ExecContext(context.WithoutCancel(p.Ctx), "SELECT pg_advisory_unlock(718283)")
			lock.Close()
		}
		defer release()
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		query = "SELECT l.k,pg_advisory_xact_lock(718283) FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k"
		if insert {
			// RETURNING blocks only after a real foreign row reaches the local
			// write. Exec must drain it and may publish neither a result nor count.
			query = "INSERT INTO fdw_push_target SELECT l.k,l.v FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k RETURNING pg_advisory_xact_lock(718283)"
		}
	}
	if analyze {
		query = "EXPLAIN ANALYZE " + query
	}
	var stmt *sql.Stmt
	if prepared {
		var err error
		stmt, err = c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
	}
	entered := make(chan struct{})
	var once sync.Once
	before := r.snapshot()
	var iterations, compositeBegins int
	if !b.PG {
		r.setHook(func(ctx context.Context, method string) error {
			if method == "BeginForeignQueryScan" {
				compositeBegins++
			}
			if method == "IterateForeignScan" {
				iterations++
			}
			// The hook runs after a real source FETCH. Let the first row
			// reach the local consumer before interrupting the second.
			if method == "IterateForeignScan" && iterations == 2 {
				once.Do(func() { close(entered) })
				<-ctx.Done()
				return ctx.Err()
			}
			if method == "EndForeignScan" && ctx.Err() != nil {
				return errors.New("cleanup retained canceled context")
			}
			return ctx.Err()
		})
		defer r.setHook(nil)
	}
	done := make(chan error, 1)
	go func() {
		if insert && !analyze {
			var result sql.Result
			var err error
			if stmt != nil {
				result, err = stmt.ExecContext(ctx)
			} else {
				result, err = c.ExecContext(ctx, query)
			}
			if result != nil {
				err = fmt.Errorf("canceled composite INSERT published affected result: %v (error %v)", result, err)
			}
			done <- err
			return
		}
		var rows *sql.Rows
		var err error
		if stmt != nil {
			rows, err = stmt.QueryContext(ctx)
		} else {
			rows, err = c.QueryContext(ctx, query)
		}
		if rows != nil {
			rows.Close()
			if err == nil {
				err = errors.New("canceled composite query published rows")
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
	if b.PG {
		for {
			var waiting bool
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT COALESCE((SELECT state='active' AND wait_event='advisory' AND query=$2 FROM pg_stat_activity WHERE pid=$1),false)", pid, query).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case err := <-done:
				joined = true
				t.Fatal("query ended before actual post-source admission", err)
			default:
			}
			runtime.Gosched()
		}
		var signaled bool
		if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
			t.Fatal("cancel admitted query", signaled, err)
		}
	} else {
		select {
		case <-entered:
		case err := <-done:
			joined = true
			t.Fatal("query ended before real source admission", err)
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		cancel()
	}
	err := <-done
	joined = true
	if b.PG {
		want := fdwDiagnostic("57014", "canceling statement due to user request")
		fixture.AssertDiagnostic(t, err, &want)
	} else {
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fdwReleased(t, r, before, 1)
		after := r.snapshot()
		if compositeBegins != 1 || after.iterations-before.iterations < 2 {
			t.Fatal("cancellation did not consume the selected composite source", before, after)
		}
	}
}

func fdwPushdownSourceReScan(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	var request stuntgres.ForeignQueryRequest
	r.mu.Lock()
	r.queryRequestHook = func(value stuntgres.ForeignQueryRequest) { request = value }
	r.mu.Unlock()
	query := "SELECT l.k,l.v,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k WHERE l.v>$1 ORDER BY l.k,l.v,r.w"
	fdwPushdownValue(run, query, []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}}, false, int64(0))
	r.mu.Lock()
	r.queryRequestHook = nil
	r.mu.Unlock()
	if len(request.Sources) == 0 {
		t.Fatal("parameterized composite did not reach real SQL source")
	}
	before := r.snapshot()
	scan, err := r.BeginForeignQueryScan(p.Ctx, request, stuntgres.ForeignQueryPlan{Slots: request.Stage.Slots})
	if err != nil {
		if scan != nil {
			_ = scan.EndForeignScan(context.WithoutCancel(p.Ctx))
		}
		t.Fatal(err)
	}
	ended := false
	defer func() {
		if !ended {
			_ = scan.EndForeignScan(context.WithoutCancel(p.Ctx))
		}
	}()
	row, present, err := scan.IterateForeignScan(p.Ctx)
	if err != nil || !present || len(row) < 4 || row[0] != int64(1) || row[1] != int64(10) || row[2] != int64(1) || row[3] != int64(100) {
		t.Fatal("real initial source row", row, present, err)
	}
	parameters := request.Parameters
	parameters.Values = append([]driver.Value{}, parameters.Values...)
	parameters.Values[0] = int64(50)
	if err := scan.ReScanForeignScan(p.Ctx, parameters); err != nil {
		t.Fatal(err)
	}
	if row, present, err := scan.IterateForeignScan(p.Ctx); err != nil || present {
		t.Fatal("changed-parameter source ReScan", row, present, err)
	}
	err = scan.EndForeignScan(context.WithoutCancel(p.Ctx))
	ended = true
	if err != nil {
		t.Fatal(err)
	}
	after := fdwReleased(t, r, before, 1)
	if after.rescans-before.rescans != 1 {
		t.Fatal("real source cursor did not rescan")
	}
}
