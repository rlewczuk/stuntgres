package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

// A session keeps real held statements on their original connection and recorder.
// It supplies individual operations, never dispatches a scenario.
type sequencePreparedSession struct {
	t       *testing.T
	p       *referencePair
	backend referenceBackend
	c       *sql.Conn
	queries map[*sql.Stmt]string
}

func newSequencePreparedSession(t *testing.T, p *referencePair, backend referenceBackend) *sequencePreparedSession {
	t.Helper()
	c, err := backend.db.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	w := &sequencePreparedSession{t: t, p: p, backend: backend, c: c, queries: make(map[*sql.Stmt]string)}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(func() {
		queries := []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "SET search_path TO public", "DROP SCHEMA IF EXISTS a,b,moved,seq_path_reader,seq_path_reader2 CASCADE", "DROP SEQUENCE IF EXISTS public.no_shadow"}
		if backend.pg {
			queries = append(queries, "DROP ROLE IF EXISTS seq_path_reader,seq_path_reader2")
		}
		// Native roles belong to this fixture's fresh driver, not a shared server.
		for _, q := range queries {
			if _, err := c.ExecContext(p.ctx, q); err != nil {
				t.Error("prepared teardown", q, err)
			}
		}
	})
	return w
}

func (w *sequencePreparedSession) exec(q string) {
	w.t.Helper()
	referenceRunStep(w.t, w.p.ctx, w.c, w.backend.recorder, referenceStep{query: q, affected: 0})
}

func (w *sequencePreparedSession) hold(q string) *sql.Stmt {
	w.t.Helper()
	s, err := w.c.PrepareContext(w.p.ctx, q)
	if err != nil {
		w.t.Fatal(q, err)
	}
	w.queries[s] = q
	w.t.Cleanup(func() {
		if err := s.Close(); err != nil {
			w.t.Error(err)
		}
	})
	return s
}

func (w *sequencePreparedSession) held(s *sql.Stmt, step referenceStep) referenceObserved {
	w.t.Helper()
	step.query = w.queries[s]
	return referenceRunStep(w.t, w.p.ctx, referenceHeldStatement{referenceExecutor: w.c, statement: s, query: step.query}, w.backend.recorder, step)
}

func (w *sequencePreparedSession) value(s *sql.Stmt, want int64) {
	w.t.Helper()
	label := "nextval"
	q := w.queries[s]
	if q == "SELECT currval('s')" {
		label = "currval"
	}
	w.held(s, sequenceInt64Step(referenceStep{selectRows: true, columns: []string{label}, types: []string{"INT8"}, rows: [][]any{{want}}}))
}

func (w *sequencePreparedSession) failure(s *sql.Stmt, code, message, position string) {
	w.t.Helper()
	want := controlsReferenceDiagnostic(code, message)
	want.Position = position
	ctx := context.WithValue(w.p.ctx, referenceRecorderKey{}, w.backend.recorder)
	var got int64
	err := s.QueryRowContext(ctx).Scan(&got)
	referenceState(w.t, err, code)
	referenceCheckDiagnostic(w.t, err, referenceStep{diagnostic: &want, errorStage: "execute"}, "execute")
}

func (w *sequencePreparedSession) shadow() *sql.Stmt {
	w.t.Helper()
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
		w.exec(q)
	}
	s := w.hold("SELECT nextval('s')")
	w.exec("CREATE SEQUENCE a.s START 100")
	return s
}

// O: obtain the identity from the actual retained PostgreSQL connection.
func sequencePreparedPID(t *testing.T, p *referencePair, c *sql.Conn) int {
	t.Helper()
	var pid int
	if err := c.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil || pid <= 0 {
		t.Fatal("actual backend PID", pid, err)
	}
	return pid
}

// Typed consumers use the same fixture contract after consuming their cursor.
func sequencePreparedRecorded(t *testing.T, backend referenceBackend, step referenceStep) {
	t.Helper()
	r := backend.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	var recorded *referenceOperation
	for _, op := range r.operations {
		if op.SQL == step.query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	if recorded == nil || !recorded.Closed || recorded.CloseErr != nil || len(recorded.Columns) != len(step.columns) {
		t.Fatal("held consumed recording", recorded)
	}
	for i, name := range step.columns {
		if recorded.Columns[i] != name {
			t.Fatal("recorded columns", recorded.Columns, step.columns)
		}
	}
	values := make([][]any, 0, len(recorded.Rows))
	for _, row := range recorded.Rows {
		if len(row) != len(step.types) {
			t.Fatal("recorded width", row, step.types)
		}
		v := make([]any, len(row))
		for i, cell := range row {
			referenceAssertRawValue(t, backend.pg, step.types[i], cell)
			v[i] = referenceNormalize(cell)
		}
		values = append(values, v)
	}
	if !referenceRecordedRowsMatch(values, step) {
		t.Fatal("recorded rows", values, step.rows)
	}
}
