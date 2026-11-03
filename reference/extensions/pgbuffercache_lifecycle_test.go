package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgBuffercacheLifecycle(t *testing.T) {
	bcScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bcDDL(t, p, b, c, "CREATE TABLE bc_part.lifecycle_heap(k integer)")
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.lifecycle_heap VALUES(1)", 1))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.lifecycle_heap")
		node, space, database := bcStorage(t, p, c, "bc_part.lifecycle_heap")
		oldKey := func() [][]any {
			t.Helper()
			return bcQuery(t, p, b, c, "SELECT * FROM bc_part.pg_buffercache WHERE relfilenode=$1::oid AND reltablespace=$2::oid AND reldatabase=$3::oid", true, node, space, database)
		}
		bcUsage(t, p, b, c, "bc_part.lifecycle_heap", 1)
		bcDDL(t, p, b, c, "ALTER TABLE bc_part.lifecycle_heap RENAME TO lifecycle_kept")
		got, _, _ := bcStorage(t, p, c, "bc_part.lifecycle_kept")
		if got != node {
			t.Fatal("rename changed storage locator", node, got)
		}
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 1)
		viewSQL := "SELECT * FROM bc_part.pg_buffercache"
		functionSQL := "SELECT * FROM " + bcFunction()
		for _, q := range []string{viewSQL, functionSQL} {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				rows, err := st.QueryContext(p.Ctx)
				if err != nil {
					st.Close()
					t.Fatal(err)
				}
				bcMetadata(t, rows, b.PG, q == viewSQL, bcNames, bcTypes)
				if !rows.Next() {
					rows.Close()
					st.Close()
					t.Fatal("provider cursor empty", rows.Err())
				}
				if err := rows.Close(); err != nil {
					st.Close()
					t.Fatal(err)
				}
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
		}
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 1)
		bcDDL(t, p, b, c, "TRUNCATE bc_part.lifecycle_kept")
		newNode, _, _ := bcStorage(t, p, c, "bc_part.lifecycle_kept")
		if newNode == node {
			t.Fatal("TRUNCATE did not replace genuine storage incarnation", node)
		}
		if rows := oldKey(); len(rows) != 0 {
			t.Fatal("TRUNCATE retained old storage", rows)
		}
		bcBoth(t, p, b, c, fixture.Select("SELECT k FROM bc_part.lifecycle_kept", []string{"k"}, []string{"INT4"}, nil))
		if !b.PG {
			bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 0)
		}
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.lifecycle_kept VALUES(1)", 1))
		bcRun(t, p, b, c, fixture.Prepared(fixture.Select("SELECT k FROM bc_part.lifecycle_kept", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}})))
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 1)
		node = newNode
		bcDDL(t, p, b, c, "DROP TABLE bc_part.lifecycle_kept", "CREATE TABLE bc_part.lifecycle_kept(k integer)")
		if rows := oldKey(); len(rows) != 0 {
			t.Fatal("DROP/recreate retained dead storage", rows)
		}
		bcRun(t, p, b, c, fixture.Exec("INSERT INTO bc_part.lifecycle_kept VALUES(1)", 1))
		bcReadHeap(t, p, b, c, "SELECT k FROM bc_part.lifecycle_kept")
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 1)
		if b.PG {
			return
		}

		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, q := range []string{viewSQL, functionSQL} {
			rows, err := c.QueryContext(ctx, q)
			if rows != nil {
				rows.Close()
				t.Fatal("pre-canceled Query exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary Query cancellation", err)
			}
			st, err := c.PrepareContext(ctx, q)
			if st != nil {
				st.Close()
				t.Fatal("pre-canceled Prepare exposed statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary Prepare cancellation", err)
			}
			st, err = c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			rows, err = st.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				st.Close()
				t.Fatal("pre-canceled Execute exposed rows")
			}
			if !errors.Is(err, context.Canceled) {
				st.Close()
				t.Fatal("ordinary Execute cancellation", err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
		}
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 1)
		// Public native backpressure owns actual reached scan sources. The two
		// host pauses have no fictional PostgreSQL shared-memory equivalent.
		stopA := bcPausedQuery(t, p, b, "SELECT k FROM bc_part.lifecycle_kept WHERE k::text LIKE '1'", false)
		bcPins(t, p, b, c, 1)
		stopB := bcPausedQuery(t, p, b, "SELECT k FROM bc_part.lifecycle_kept WHERE k::text LIKE '1'", true)
		bcPins(t, p, b, c, 2)
		detached := bcTarget(t, p, b, c, "bc_part.lifecycle_kept")
		if err := stopA(); !errors.Is(err, context.Canceled) {
			t.Fatal("admitted first owner cancel", err)
		}
		bcPins(t, p, b, c, 1)
		if detached[0][8] != int64(2) {
			t.Fatal("old snapshot borrowed active pin state", detached)
		}
		if err := stopB(); !errors.Is(err, context.Canceled) {
			t.Fatal("admitted second owner cancel", err)
		}
		bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 3)
		for _, prepared := range []bool{false, true} {
			stop := bcPausedQuery(t, p, b, "SELECT bufferid FROM "+bcFunction()+" WHERE bufferid::text LIKE '1'", prepared)
			if err := stop(); !errors.Is(err, context.Canceled) {
				t.Fatal("admitted provider cursor cancel", err)
			}
			bcUsage(t, p, b, c, "bc_part.lifecycle_kept", 3)
		}
	})
}

func bcPins(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, want int64) {
	t.Helper()
	rows := bcTarget(t, p, b, c, "bc_part.lifecycle_kept")
	if len(rows) != 1 || rows[0][8] != want {
		t.Fatal("distinct active source-owning sessions", rows, "want", want)
	}
}

func bcPausedQuery(t *testing.T, p *fixture.Pair, b fixture.Backend, query string, prepared bool) func() error {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	var st *sql.Stmt
	if prepared {
		st, err = c.PrepareContext(p.Ctx, query)
		if err != nil {
			c.Close()
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	pause := stuntgres.NewExecutionPause("text.like.match")
	ctx = stuntgres.WithExecutionPause(ctx, pause)
	done := make(chan error, 1)
	go func() {
		var rows *sql.Rows
		var err error
		if st != nil {
			rows, err = st.QueryContext(ctx)
		} else {
			rows, err = c.QueryContext(ctx, query)
		}
		if err == nil {
			for rows.Next() {
				var k int64
				if err = rows.Scan(&k); err != nil {
					break
				}
			}
			if err == nil {
				err = rows.Err()
			}
			err = errors.Join(err, rows.Close())
		}
		done <- err
	}()
	finished := false
	var result error
	stop := func() error {
		if !finished {
			cancel()
			pause.Resume()
			timer := time.NewTimer(fixture.Liveness)
			defer timer.Stop()
			select {
			case result = <-done:
				if st != nil {
					if err := st.Close(); err != nil {
						t.Error(err)
					}
				}
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			case <-timer.C:
				result = errors.New("canceled buffercache source owner failed to join")
				t.Error(result)
				go func() {
					if st != nil {
						st.Close()
					}
					c.Close()
				}()
			}
			finished = true
		}
		return result
	}
	t.Cleanup(func() { stop() })
	waitCtx, waitCancel := context.WithTimeout(p.Ctx, fixture.Liveness)
	defer waitCancel()
	if err := pause.Wait(waitCtx); err != nil {
		t.Fatal("actual eligible source did not reach runtime work", err)
	}
	return stop
}
