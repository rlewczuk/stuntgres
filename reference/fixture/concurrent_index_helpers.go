package fixture

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func ConcurrentIndexScenario(t *testing.T, scenario func(*testing.T, *Pair, Backend)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { scenario(t, p, backend) })
	}
}

func ConcurrentIndexConn(t *testing.T, p *Pair, b Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.ExecContext(context.Background(), "ROLLBACK"); _ = c.Close() })
	return c
}

func ConcurrentIndexExec(t *testing.T, p *Pair, b Backend, x Executor, q string, count int64) {
	t.Helper()
	RunStep(t, p.Ctx, x, b.Recorder, Exec(q, count))
}

func ConcurrentIndexError(t *testing.T, p *Pair, b Backend, x Executor, q string, want Diagnostic) {
	t.Helper()
	_, err := x.ExecContext(p.Ctx, q)
	AssertDiagnostic(t, err, &want)
}

func ConcurrentIndexUnique(table, index, detail string, building bool) Diagnostic {
	message := "duplicate key value violates unique constraint \"" + index + "\""
	if building {
		message = "could not create unique index \"" + index + "\""
	}
	d := ControlsReferenceDiagnostic("23505", message)
	d.Detail, d.Schema, d.Table, d.Constraint = detail, "public", table, index
	return d
}

func ConcurrentIndexState(t *testing.T, p *Pair, b Backend, name string) (stuntgres.IndexState, bool) {
	t.Helper()
	if !b.PG {
		s, err := stuntgres.InspectIndex(p.Ctx, p.Native, "public", name)
		if err != nil {
			if err.Error() == "stuntgres: index \"public\".\""+name+"\" does not exist" {
				return s, false
			}
			t.Fatal(err)
		}
		return s, true
	}
	var s stuntgres.IndexState
	err := Admin.QueryRowContext(p.Ctx, `SELECT indisready, indisvalid, indislive FROM pg_index JOIN pg_class ON indexrelid=pg_class.oid JOIN pg_namespace ON relnamespace=pg_namespace.oid WHERE nspname='public' AND relname=$1`, name).Scan(&s.Ready, &s.Valid, &s.Live)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return s, true
}

func ConcurrentIndexWait(t *testing.T, p *Pair, b Backend, name string, want stuntgres.IndexState, pid, holder int, phase string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	for {
		s, found := ConcurrentIndexState(t, p, b, name)
		durable := s
		durable.WaitingForOlderSnapshots, durable.WaitingForWriters = false, false
		flagsMatch := durable == want
		if found && flagsMatch {
			if !b.PG {
				switch phase {
				case "waiting for old snapshots":
					if s.WaitingForOlderSnapshots {
						return
					}
				case "waiting for writers before build", "waiting for writers before validation":
					if s.WaitingForWriters {
						return
					}
				default:
					if !s.WaitingForOlderSnapshots && !s.WaitingForWriters {
						return
					}
				}
			} else if phase == "" {
				return
			}
			if b.PG {
				var waiting bool
				if err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_progress_create_index WHERE pid=$1 AND phase=$2 AND current_locker_pid=$3)`, pid, phase, holder).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					return
				}
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("index %s: want %+v phase %s: %v", name, want, phase, ctx.Err())
		}
		runtime.Gosched()
	}
}

type concurrentIndexWorker struct {
	Done   chan error
	Cancel context.CancelFunc
}

func ConcurrentIndexStart(t *testing.T, p *Pair, c *sql.Conn, q string, held bool) *concurrentIndexWorker {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	var stmt *sql.Stmt
	if held {
		var err error
		stmt, err = c.PrepareContext(ctx, q)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stmt.Close() })
		words := strings.Fields(q)
		for i, word := range words {
			if word == "ON" && i > 0 {
				RunStep(t, ctx, c, &Recorder{}, Select("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, nil, words[i-1]))
				break
			}
		}
	}
	w := &concurrentIndexWorker{make(chan error, 1), cancel}
	go func() {
		var result sql.Result
		var err error
		if stmt != nil {
			result, err = stmt.ExecContext(ctx)
			_ = stmt.Close()
		} else {
			result, err = c.ExecContext(ctx, q)
		}
		if err == nil {
			var n int64
			n, err = result.RowsAffected()
			if err == nil && n != 0 {
				err = errors.New("CREATE INDEX affected nonzero rows")
			}
			if _, last := result.LastInsertId(); err == nil && last == nil {
				err = errors.New("CREATE INDEX exposed LastInsertId")
			}
		} else if result != nil {
			err = errors.New("failed CREATE INDEX published a result")
		}
		w.Done <- err
		close(w.Done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-w.Done:
		case <-time.After(5 * time.Second):
			t.Error("index worker did not join")
		}
	})
	return w
}
func (w *concurrentIndexWorker) Join(t *testing.T, p *Pair) error {
	t.Helper()
	select {
	case err := <-w.Done:
		return err
	case <-p.Ctx.Done():
		t.Fatal(p.Ctx.Err())
		return p.Ctx.Err()
	}
}
func ConcurrentIndexPID(t *testing.T, p *Pair, b Backend, c *sql.Conn) int {
	if b.PG {
		return LockPID(t, p.Ctx, c)
	}
	return 0
}
func ConcurrentIndexRows(t *testing.T, p *Pair, b Backend, x Executor, q string, values ...int64) {
	t.Helper()
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	RunStep(t, p.Ctx, x, b.Recorder, Select(q, []string{"n"}, []string{"INT4"}, rows))
}
