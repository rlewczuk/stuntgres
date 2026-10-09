package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TEMP tests keep physical owners explicit; every selected pool uses its shipped recorder.
type tempReferenceSession struct {
	t                  *testing.T
	p                  *referencePair
	db                 *sql.DB
	c, peer            *sql.Conn
	record             *referenceRecorder
	prepared, postgres bool
}

func newTempReferenceSession(t *testing.T, p *referencePair, db *sql.DB, record *referenceRecorder, prepared bool) *tempReferenceSession {
	t.Helper()
	c, peer := referenceLockConnections(t, p.ctx, db)
	return &tempReferenceSession{t: t, p: p, db: db, c: c, peer: peer, record: record, prepared: prepared, postgres: db == p.postgres}
}

func (s *tempReferenceSession) exec(c referenceExecutor, q string, count int64) {
	s.t.Helper()
	referenceRunStep(s.t, s.p.ctx, c, s.record, referenceStep{query: q, affected: count})
}

func (s *tempReferenceSession) value(c referenceExecutor, q string, want int64) {
	s.t.Helper()
	column := strings.TrimPrefix(q, "SELECT ")
	if i := strings.Index(column, " FROM "); i >= 0 {
		column = column[:i]
	}
	if i := strings.Index(column, "("); i >= 0 {
		column = column[:i]
	}
	referenceRunStep(s.t, s.p.ctx, c, s.record, sequenceInt64Step(referenceStep{query: q, selectRows: true, columns: []string{column}, types: []string{"INT8"}, rows: [][]any{{want}}}))
}

func (s *tempReferenceSession) utility(q string, diagnostic *referenceDiagnostic) {
	s.t.Helper()
	step := referenceStep{query: q, prepared: s.prepared, diagnostic: diagnostic}
	if diagnostic != nil {
		step.state, step.errorStage = diagnostic.Code, "execute"
	}
	referenceRunStep(s.t, s.p.ctx, s.c, s.record, step)
}

func tempReferenceDiagnostic(code, message, detail, position string) *referenceDiagnostic {
	d := controlsReferenceDiagnostic(code, message)
	d.Detail, d.Position = detail, position
	return &d
}

func (s *tempReferenceSession) missing(c referenceExecutor, name, column, position string) {
	s.t.Helper()
	d := tempReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`, "", position)
	referenceRunStep(s.t, s.p.ctx, c, s.record, referenceStep{query: "SELECT " + column + " FROM " + name, selectRows: true, state: d.Code, diagnostic: d, errorStage: "execute"})
}

func (s *tempReferenceSession) held(c *sql.Conn, q string) referenceHeldStatement {
	s.t.Helper()
	stmt, err := c.PrepareContext(s.p.ctx, q)
	if err != nil {
		s.t.Fatal(q, err)
	}
	s.t.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			s.t.Error(err)
		}
	})
	return referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
}

func (s *tempReferenceSession) heldValues(h referenceHeldStatement, columns []string, values ...int64) {
	s.t.Helper()
	types, row := make([]string, len(values)), make([]any, len(values))
	for i, value := range values {
		types[i], row[i] = "INT8", value
	}
	referenceRunStep(s.t, s.p.ctx, h, s.record, sequenceInt64Step(referenceStep{query: h.query, selectRows: true, columns: columns, types: types, rows: [][]any{row}}))
}

// log_cnt is a physical PG counter: assert nonnegative and preservation, never native/PG equality.
func (s *tempReferenceSession) state(c *sql.Conn, name string, last int64, called bool) int64 {
	s.t.Helper()
	q := "SELECT last_value,log_cnt,is_called FROM " + name
	ctx := context.WithValue(s.p.ctx, referenceRecorderKey{}, s.record)
	rows, err := c.QueryContext(ctx, q)
	if err != nil {
		s.t.Fatal(q, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"last_value", "log_cnt", "is_called"}) {
		s.t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		s.t.Fatal(types, err)
	}
	for i, want := range []string{"INT8", "INT8", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			s.t.Fatal(types[i])
		}
	}
	if !rows.Next() {
		s.t.Fatal("missing state", rows.Err())
	}
	var a, log, b any
	if err := rows.Scan(&a, &log, &b); err != nil {
		s.t.Fatal(err)
	}
	rawLast, lastOK := a.(int64)
	n, logOK := log.(int64)
	rawCalled, calledOK := b.(bool)
	if !lastOK || !logOK || !calledOK || rawLast != last || rawCalled != called || n < 0 || (!s.postgres && n != 0) {
		s.t.Fatal(name, a, log, b)
	}
	var typedLast, typedLog int64
	var typedCalled bool
	if err := rows.Scan(&typedLast, &typedLog, &typedCalled); err != nil {
		s.t.Fatal(err)
	}
	if typedLast != last || typedCalled != called || typedLog != n || typedLog < 0 || (!s.postgres && typedLog != 0) {
		s.t.Fatal("typed state", name, typedLast, typedLog, typedCalled)
	}
	if rows.Next() || rows.Err() != nil {
		s.t.Fatal("state exhaustion", rows.Err())
	}
	if err := rows.Close(); err != nil {
		s.t.Fatal(err)
	}
	s.record.mu.Lock()
	defer s.record.mu.Unlock()
	var op *referenceOperation
	for _, candidate := range s.record.operations {
		if candidate.SQL == q && candidate.Err == nil && candidate.Columns != nil {
			op = candidate
		}
	}
	if op == nil || !op.Closed || op.CloseErr != nil || !reflect.DeepEqual(op.Columns, columns) || !reflect.DeepEqual(op.Rows, [][]driver.Value{{last, n, called}}) {
		s.t.Fatal("recorded state/closure", op)
	}
	return n
}

func (s *tempReferenceSession) schemas(names ...string) {
	s.t.Helper()
	// Registered after connection cleanup: run while the peer still owns its physical session.
	s.t.Cleanup(func() {
		for _, name := range names {
			if _, err := s.peer.ExecContext(s.p.ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
				s.t.Error(err)
			}
		}
	})
}

func (s *tempReferenceSession) retire() {
	s.t.Helper()
	var pid int
	if s.postgres {
		pid = sequenceOwnedLockPID(s.t, s.p.ctx, s.c)
	}
	s.db.SetMaxIdleConns(0)
	if err := s.c.Close(); err != nil {
		s.t.Fatal(err)
	}
	if !s.postgres {
		return
	}
	ctx, cancel := context.WithTimeout(s.p.ctx, 2*time.Second)
	defer cancel()
	for {
		var live bool
		if err := s.peer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
			s.t.Fatal("observing TEMP owner exit", err)
		}
		if !live {
			return
		}
	}
}

func (s *tempReferenceSession) snapshot(rows *sql.Rows, q string) {
	s.t.Helper()
	frozen := sequenceFrozenRows{referenceExecutor: s.c, rows: rows, query: q}
	step := referenceStep{query: q, selectRows: true, columns: []string{"last_value", "is_called"}, types: []string{"INT8", "BOOL"}, rows: [][]any{{int64(35), true}}}
	step.checkScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
		t.Helper()
		if rowIndex != 0 {
			t.Fatalf("unexpected frozen row %d", rowIndex)
		}
		var last int64
		var called bool
		if err := rows.Scan(&last, &called); err != nil {
			t.Fatal(err)
		}
		if last != 35 || !called {
			t.Fatal("typed frozen state", last, called)
		}
	}
	referenceRunStep(s.t, s.p.ctx, frozen, s.record, step)
}
