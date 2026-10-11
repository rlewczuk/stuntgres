package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// A session keeps real held statements on their original connection and recorder.
// It supplies individual operations, never dispatches a scenario.
type sequencePreparedSession struct {
	t       *testing.T
	p       *fixture.Pair
	backend fixture.Backend
	c       *sql.Conn
	queries map[*sql.Stmt]string
}

func newSequencePreparedSession(t *testing.T, p *fixture.Pair, backend fixture.Backend) *sequencePreparedSession {
	t.Helper()
	c, err := backend.DB.Conn(p.Ctx)
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
		if backend.PG {
			queries = append(queries, "DROP ROLE IF EXISTS seq_path_reader,seq_path_reader2")
		}
		// Native roles belong to this fixture's fresh driver, not a shared server.
		for _, q := range queries {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Error("prepared teardown", q, err)
			}
		}
	})
	return w
}

func (w *sequencePreparedSession) exec(q string) {
	w.t.Helper()
	fixture.RunStep(w.t, w.p.Ctx, w.c, w.backend.Recorder, fixture.Step{Query: q, Affected: 0})
}

func (w *sequencePreparedSession) hold(q string) *sql.Stmt {
	w.t.Helper()
	s, err := w.c.PrepareContext(w.p.Ctx, q)
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

func (w *sequencePreparedSession) held(s *sql.Stmt, step fixture.Step) fixture.Observed {
	w.t.Helper()
	step.Query = w.queries[s]
	return fixture.RunStep(w.t, w.p.Ctx, fixture.HeldStatement{Executor: w.c, Statement: s, SQL: step.Query}, w.backend.Recorder, step)
}

func (w *sequencePreparedSession) value(s *sql.Stmt, want int64) {
	w.t.Helper()
	label := "nextval"
	q := w.queries[s]
	if q == "SELECT currval('s')" {
		label = "currval"
	}
	w.held(s, fixture.SequenceInt64Step(fixture.Step{SelectRows: true, Columns: []string{label}, Types: []string{"INT8"}, Rows: [][]any{{want}}}))
}

func (w *sequencePreparedSession) failure(s *sql.Stmt, code, message, position string) {
	w.t.Helper()
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Position = position
	ctx := context.WithValue(w.p.Ctx, fixture.RecorderKey{}, w.backend.Recorder)
	var got int64
	err := s.QueryRowContext(ctx).Scan(&got)
	fixture.State(w.t, err, code)
	fixture.CheckDiagnostic(w.t, err, fixture.Step{Diagnostic: &want, ErrorStage: "execute"}, "execute")
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
func sequencePreparedPID(t *testing.T, p *fixture.Pair, c *sql.Conn) int {
	t.Helper()
	var pid int
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil || pid <= 0 {
		t.Fatal("actual backend PID", pid, err)
	}
	return pid
}

// Typed consumers use the same fixture contract after consuming their cursor.
func sequencePreparedRecorded(t *testing.T, backend fixture.Backend, step fixture.Step) {
	t.Helper()
	r := backend.Recorder
	r.Mu.Lock()
	defer r.Mu.Unlock()
	var recorded *fixture.Operation
	for _, op := range r.Operations {
		if op.SQL == step.Query && op.Err == nil && op.Columns != nil {
			recorded = op
		}
	}
	if recorded == nil || !recorded.Closed || recorded.CloseErr != nil || len(recorded.Columns) != len(step.Columns) {
		t.Fatal("held consumed recording", recorded)
	}
	for i, name := range step.Columns {
		if recorded.Columns[i] != name {
			t.Fatal("recorded columns", recorded.Columns, step.Columns)
		}
	}
	values := make([][]any, 0, len(recorded.Rows))
	for _, row := range recorded.Rows {
		if len(row) != len(step.Types) {
			t.Fatal("recorded width", row, step.Types)
		}
		v := make([]any, len(row))
		for i, cell := range row {
			fixture.AssertRawValue(t, backend.PG, step.Types[i], cell)
			v[i] = fixture.Normalize(cell)
		}
		values = append(values, v)
	}
	if !fixture.RecordedRowsMatch(values, step) {
		t.Fatal("recorded rows", values, step.Rows)
	}
}
