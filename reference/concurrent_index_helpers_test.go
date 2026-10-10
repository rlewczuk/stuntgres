package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"stuntgres"
)

func concurrentIndexScenario(t *testing.T, scenario func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { scenario(t, p, backend) })
	}
}

func concurrentIndexConn(t *testing.T, p *referencePair, b referenceBackend) *sql.Conn {
	t.Helper()
	c, err := b.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = c.ExecContext(context.Background(), "ROLLBACK"); _ = c.Close() })
	return c
}

func concurrentIndexExec(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, count int64) {
	t.Helper()
	referenceRunStep(t, p.ctx, x, b.recorder, referenceExec(q, count))
}

func concurrentIndexError(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, want referenceDiagnostic) {
	t.Helper()
	_, err := x.ExecContext(p.ctx, q)
	referenceAssertDiagnostic(t, err, &want)
}

func concurrentIndexUnique(table, index, detail string, building bool) referenceDiagnostic {
	message := "duplicate key value violates unique constraint \"" + index + "\""
	if building {
		message = "could not create unique index \"" + index + "\""
	}
	d := controlsReferenceDiagnostic("23505", message)
	d.Detail, d.Schema, d.Table, d.Constraint = detail, "public", table, index
	return d
}

func concurrentIndexState(t *testing.T, p *referencePair, b referenceBackend, name string) (stuntgres.IndexState, bool) {
	t.Helper()
	if !b.pg {
		s, err := stuntgres.InspectIndex(p.ctx, p.native, "public", name)
		if err != nil {
			if err.Error() == "stuntgres: index \"public\".\""+name+"\" does not exist" {
				return s, false
			}
			t.Fatal(err)
		}
		return s, true
	}
	var s stuntgres.IndexState
	err := referenceAdmin.QueryRowContext(p.ctx, `SELECT indisready, indisvalid, indislive FROM pg_index JOIN pg_class ON indexrelid=pg_class.oid JOIN pg_namespace ON relnamespace=pg_namespace.oid WHERE nspname='public' AND relname=$1`, name).Scan(&s.Ready, &s.Valid, &s.Live)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return s, true
}

func concurrentIndexWait(t *testing.T, p *referencePair, b referenceBackend, name string, want stuntgres.IndexState, pid, holder int, phase string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
	defer cancel()
	for {
		s, found := concurrentIndexState(t, p, b, name)
		durable := s
		durable.WaitingForOlderSnapshots, durable.WaitingForWriters = false, false
		flagsMatch := durable == want
		if found && flagsMatch {
			if !b.pg {
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
			if b.pg {
				var waiting bool
				if err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_progress_create_index WHERE pid=$1 AND phase=$2 AND current_locker_pid=$3)`, pid, phase, holder).Scan(&waiting); err != nil {
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
	done   chan error
	cancel context.CancelFunc
}

func concurrentIndexStart(t *testing.T, p *referencePair, c *sql.Conn, q string, held bool) *concurrentIndexWorker {
	t.Helper()
	ctx, cancel := context.WithCancel(p.ctx)
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
				referenceRunStep(t, ctx, c, &referenceRecorder{}, referenceSelect("SELECT relname FROM pg_class WHERE relname=$1", []string{"relname"}, []string{"NAME"}, nil, words[i-1]))
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
		w.done <- err
		close(w.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-w.done:
		case <-time.After(5 * time.Second):
			t.Error("index worker did not join")
		}
	})
	return w
}
func (w *concurrentIndexWorker) join(t *testing.T, p *referencePair) error {
	t.Helper()
	select {
	case err := <-w.done:
		return err
	case <-p.ctx.Done():
		t.Fatal(p.ctx.Err())
		return p.ctx.Err()
	}
}
func concurrentIndexPID(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) int {
	if b.pg {
		return referenceLockPID(t, p.ctx, c)
	}
	return 0
}
func concurrentIndexRows(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, values ...int64) {
	t.Helper()
	rows := make([][]any, len(values))
	for i, v := range values {
		rows[i] = []any{v}
	}
	referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect(q, []string{"n"}, []string{"INT4"}, rows))
}
