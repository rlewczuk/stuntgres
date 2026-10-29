package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const vacuumProgressQuery = "SELECT * FROM pg_catalog.pg_stat_progress_vacuum WHERE pid=$1"
const vacuumProgressVersions = 262144

type vacuumProgressSnapshot struct {
	pid, datid int64
	database   string
	relid      sql.NullInt64
	phase      sql.NullString
	counters   [9]sql.NullInt64
	delay      sql.NullFloat64
}

func vacuumProgressScan(t *testing.T, row *sql.Row) (vacuumProgressSnapshot, bool) {
	t.Helper()
	var s vacuumProgressSnapshot
	args := []any{&s.pid, &s.datid, &s.database, &s.relid, &s.phase}
	for i := range s.counters {
		args = append(args, &s.counters[i])
	}
	args = append(args, &s.delay)
	if err := row.Scan(args...); err != nil {
		if err == sql.ErrNoRows {
			return s, false
		}
		t.Fatal(err)
	}
	return s, true
}

func vacuumProgressVisible(t *testing.T, s vacuumProgressSnapshot, id copyProgressIdentity, pg bool) {
	t.Helper()
	if s.pid != id.pid || s.datid != id.datid || s.database != id.database || !s.relid.Valid || s.relid.Int64 != id.relid || !s.phase.Valid {
		t.Fatalf("progress=%+v identity=%+v", s, id)
	}
	switch s.phase.String {
	case "initializing", "scanning heap", "vacuuming indexes", "vacuuming heap", "cleaning up indexes", "truncating heap", "performing final cleanup":
	default:
		t.Fatalf("unknown phase %+v", s)
	}
	for _, n := range s.counters {
		if !n.Valid || n.Int64 < 0 {
			t.Fatalf("authorized numeric NULL/negative: %+v", s)
		}
	}
	for _, pair := range [][2]int{{0, 1}, {0, 2}, {4, 5}, {7, 8}} {
		if s.counters[pair[1]].Int64 > s.counters[pair[0]].Int64 {
			t.Fatalf("completed work exceeds total: %+v", s)
		}
	}
	if !s.delay.Valid || math.IsNaN(s.delay.Float64) || math.IsInf(s.delay.Float64, 0) || s.delay.Float64 < 0 || (!pg && s.delay.Float64 != 0) {
		t.Fatalf("delay %+v", s)
	}
}

func vacuumProgressMasked(t *testing.T, s vacuumProgressSnapshot, id copyProgressIdentity) {
	t.Helper()
	if s.pid != id.pid || s.datid != id.datid || s.database != id.database || s.relid.Valid || s.phase.Valid || s.delay.Valid {
		t.Fatalf("restricted row %+v", s)
	}
	for _, n := range s.counters {
		if n.Valid {
			t.Fatalf("restricted numeric leaked: %+v", s)
		}
	}
}

func vacuumProgressWorkload(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	copyProgressExec(t, p.Ctx, c, "CREATE TABLE "+table+"(id INT, payload TEXT)")
	if b.PG {
		copyProgressExec(t, p.Ctx, c, "ALTER TABLE "+table+" SET (autovacuum_enabled=false)")
	}
	payload := strings.Repeat("v", 128)
	for first := 0; first < vacuumProgressVersions; first += 1024 {
		var q strings.Builder
		fmt.Fprintf(&q, "INSERT INTO %s VALUES", table)
		for i := first; i < first+1024; i++ {
			if i != first {
				q.WriteByte(',')
			}
			fmt.Fprintf(&q, "(%d,'%s')", i, payload)
		}
		copyProgressExec(t, p.Ctx, c, q.String())
	}
	copyProgressExec(t, p.Ctx, c, "CREATE INDEX "+table+"_id ON "+table+"(id)")
	copyProgressExec(t, p.Ctx, c, "CREATE INDEX "+table+"_payload ON "+table+"(payload)")
	copyProgressExec(t, p.Ctx, c, "DELETE FROM "+table+" WHERE id % 2 = 0")
}

type vacuumProgressRunning struct {
	cancel      context.CancelFunc
	done        chan error
	finished    chan struct{}
	joined      bool
	workerStack string
	pg, sampled bool
}

func vacuumProgressLaunch(t *testing.T, p *fixture.Pair, b fixture.Backend, worker *sql.Conn, table string) *vacuumProgressRunning {
	t.Helper()
	if b.PG {
		copyProgressExec(t, p.Ctx, worker, "SET vacuum_cost_limit=20")
		copyProgressExec(t, p.Ctx, worker, "SET vacuum_cost_delay='1ms'")
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	r := &vacuumProgressRunning{cancel: cancel, done: make(chan error, 1), finished: make(chan struct{})}
	r.pg = b.PG
	identity := make(chan string, 1)
	go func() {
		var own [256]byte
		n := runtime.Stack(own[:], false)
		header := strings.Fields(string(own[:n]))
		identity <- header[0] + " " + header[1] + " "
		_, err := worker.ExecContext(ctx, "VACUUM (PROCESS_TOAST false) "+table)
		r.done <- err
		close(r.finished)
	}()
	t.Cleanup(func() {
		cancel()
		if !r.joined {
			<-r.done
			r.joined = true
		}
	})
	r.workerStack = <-identity
	return r
}

func vacuumProgressObserve(t *testing.T, p *fixture.Pair, stmt *sql.Stmt, id copyProgressIdentity, r *vacuumProgressRunning) vacuumProgressSnapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 30*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s, ok := vacuumProgressScan(t, stmt.QueryRowContext(ctx, id.pid))
		if !r.pg && !r.sampled {
			r.sampled = vacuumProgressNativeStack(r.workerStack)
		}
		select {
		case <-r.finished:
			t.Fatal("VACUUM returned before a live second-session SQL snapshot")
		default:
		}
		if ok && (r.pg || r.sampled) {
			return s
		}
		select {
		case <-r.finished:
			t.Fatal("VACUUM completed without an observed public progress row")
		case <-ctx.Done():
			t.Fatal("no active SQL progress witness", ctx.Err())
		case <-tick.C:
		}
	}
}

// Match a complete live stack of this exact launched worker, excluding creation
// metadata, just as fixture's active-operation admission observer does.
func vacuumProgressNativeStack(worker string) bool {
	stacks := make([]byte, 64<<10)
	n := runtime.Stack(stacks, true)
	for n == len(stacks) {
		stacks = make([]byte, len(stacks)*2)
		n = runtime.Stack(stacks, true)
	}
	for _, stack := range strings.Split(string(stacks[:n]), "\n\n") {
		if !strings.HasPrefix(stack, worker) {
			continue
		}
		live, _, _ := strings.Cut(stack, "\ncreated by ")
		for _, symbol := range []string{"storage.InspectVacuum", "storage.MaterializeVacuum", "storage.RemapVacuumIndex", "index.RebuildBrinVacuumRoot"} {
			if strings.Contains(live, symbol) {
				return true
			}
		}
	}
	return false
}

func TestReferenceVacuumProgressActive(t *testing.T) {
	for _, cancelWork := range []bool{false, true} {
		name := "success"
		if cancelWork {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				setup, worker, observer := vacuumConn(t, p, b), vacuumConn(t, p, b), vacuumConn(t, p, b)
				vacuumProgressWorkload(t, p, b, setup, "vacuum_active")
				id := copyProgressIdentify(t, p, worker, "vacuum_active")
				stmt, err := observer.PrepareContext(p.Ctx, vacuumProgressQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				vacuumProgressEmpty(t, p, b, observer)
				r := vacuumProgressLaunch(t, p, b, worker, "vacuum_active")
				first := vacuumProgressObserve(t, p, stmt, id, r)
				vacuumProgressVisible(t, first, id, b.PG)
				previous := first
				if cancelWork {
					vacuumProgressCancel(t, p, b, observer, id.pid, "vacuum_active", r.cancel, func() error { err := <-r.done; r.joined = true; return err })
					vacuumCount(t, p.Ctx, observer, "vacuum_active", 0)
				} else {
					for {
						select {
						case <-r.finished:
							goto complete
						default:
						}
						s, ok := vacuumProgressScan(t, observer.QueryRowContext(p.Ctx, vacuumProgressQuery, id.pid))
						if !ok {
							continue
						}
						vacuumProgressVisible(t, s, id, b.PG)
						for _, i := range []int{0, 1, 2, 3} {
							if s.counters[i].Int64 < previous.counters[i].Int64 {
								t.Fatalf("completed gauge decreased: before=%+v after=%+v", previous, s)
							}
						}
						previous = s
					}
				complete:
					err := <-r.done
					r.joined = true
					if err != nil {
						t.Fatal(err)
					}
					vacuumCount(t, p.Ctx, observer, "vacuum_active", 1)
				}
				vacuumProgressEmpty(t, p, b, observer)
				vacuumRun(t, p, b, observer, fixture.Select("SELECT count(*) AS n FROM vacuum_active", []string{"n"}, []string{"INT8"}, [][]any{{int64(vacuumProgressVersions / 2)}}))
				vacuumRun(t, p, b, observer, fixture.Select("SELECT id FROM vacuum_active WHERE id IN (0,1,2,3) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}}))
				// Detached driver snapshots remain ordinary immutable values after cleanup.
				vacuumProgressVisible(t, first, id, b.PG)
				if !b.PG {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					vacuumProgressEmpty(t, p, b, observer)
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE vacuum_progress_restored(id INT); INSERT INTO vacuum_progress_restored VALUES(7);")); err != nil {
						t.Fatal(err)
					}
					vacuumProgressEmpty(t, p, b, observer)
					vacuumRows(t, p, b, observer, "vacuum_progress_restored", 7)
					vacuumProgressVisible(t, first, id, false)
				}
			})
		})
	}
}
