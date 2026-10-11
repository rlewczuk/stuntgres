package fixture

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
type TempReferenceSession struct {
	T                  *testing.T
	P                  *Pair
	DB                 *sql.DB
	C, Peer            *sql.Conn
	Record             *Recorder
	Prepared, Postgres bool
}

func NewTempReferenceSession(t *testing.T, p *Pair, db *sql.DB, record *Recorder, prepared bool) *TempReferenceSession {
	t.Helper()
	c, peer := LockConnections(t, p.Ctx, db)
	return &TempReferenceSession{T: t, P: p, DB: db, C: c, Peer: peer, Record: record, Prepared: prepared, Postgres: db == p.Postgres}
}

func (s *TempReferenceSession) Exec(c Executor, q string, count int64) {
	s.T.Helper()
	RunStep(s.T, s.P.Ctx, c, s.Record, Step{Query: q, Affected: count})
}

func (s *TempReferenceSession) Value(c Executor, q string, want int64) {
	s.T.Helper()
	column := strings.TrimPrefix(q, "SELECT ")
	if i := strings.Index(column, " FROM "); i >= 0 {
		column = column[:i]
	}
	if i := strings.Index(column, "("); i >= 0 {
		column = column[:i]
	}
	RunStep(s.T, s.P.Ctx, c, s.Record, SequenceInt64Step(Step{Query: q, SelectRows: true, Columns: []string{column}, Types: []string{"INT8"}, Rows: [][]any{{want}}}))
}

func (s *TempReferenceSession) Utility(q string, diagnostic *Diagnostic) {
	s.T.Helper()
	step := Step{Query: q, Prepared: s.Prepared, Diagnostic: diagnostic}
	if diagnostic != nil {
		step.State, step.ErrorStage = diagnostic.Code, "execute"
	}
	RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
}

func TempReferenceDiagnostic(code, message, detail, position string) *Diagnostic {
	d := ControlsReferenceDiagnostic(code, message)
	d.Detail, d.Position = detail, position
	return &d
}

func (s *TempReferenceSession) Missing(c Executor, name, column, position string) {
	s.T.Helper()
	d := TempReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`, "", position)
	RunStep(s.T, s.P.Ctx, c, s.Record, Step{Query: "SELECT " + column + " FROM " + name, SelectRows: true, State: d.Code, Diagnostic: d, ErrorStage: "execute"})
}

func (s *TempReferenceSession) Held(c *sql.Conn, q string) HeldStatement {
	s.T.Helper()
	stmt, err := c.PrepareContext(s.P.Ctx, q)
	if err != nil {
		s.T.Fatal(q, err)
	}
	s.T.Cleanup(func() {
		if err := stmt.Close(); err != nil {
			s.T.Error(err)
		}
	})
	return HeldStatement{Executor: c, Statement: stmt, SQL: q}
}

func (s *TempReferenceSession) HeldValues(h HeldStatement, columns []string, values ...int64) {
	s.T.Helper()
	types, row := make([]string, len(values)), make([]any, len(values))
	for i, value := range values {
		types[i], row[i] = "INT8", value
	}
	RunStep(s.T, s.P.Ctx, h, s.Record, SequenceInt64Step(Step{Query: h.SQL, SelectRows: true, Columns: columns, Types: types, Rows: [][]any{row}}))
}

// log_cnt is a physical PG counter: assert nonnegative and preservation, never native/PG equality.
func (s *TempReferenceSession) State(c *sql.Conn, name string, last int64, called bool) int64 {
	s.T.Helper()
	q := "SELECT last_value,log_cnt,is_called FROM " + name
	ctx := context.WithValue(s.P.Ctx, RecorderKey{}, s.Record)
	rows, err := c.QueryContext(ctx, q)
	if err != nil {
		s.T.Fatal(q, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"last_value", "log_cnt", "is_called"}) {
		s.T.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 3 {
		s.T.Fatal(types, err)
	}
	for i, want := range []string{"INT8", "INT8", "BOOL"} {
		if types[i].DatabaseTypeName() != want {
			s.T.Fatal(types[i])
		}
	}
	if !rows.Next() {
		s.T.Fatal("missing state", rows.Err())
	}
	var a, log, b any
	if err := rows.Scan(&a, &log, &b); err != nil {
		s.T.Fatal(err)
	}
	rawLast, lastOK := a.(int64)
	n, logOK := log.(int64)
	rawCalled, calledOK := b.(bool)
	if !lastOK || !logOK || !calledOK || rawLast != last || rawCalled != called || n < 0 || (!s.Postgres && n != 0) {
		s.T.Fatal(name, a, log, b)
	}
	var typedLast, typedLog int64
	var typedCalled bool
	if err := rows.Scan(&typedLast, &typedLog, &typedCalled); err != nil {
		s.T.Fatal(err)
	}
	if typedLast != last || typedCalled != called || typedLog != n || typedLog < 0 || (!s.Postgres && typedLog != 0) {
		s.T.Fatal("typed state", name, typedLast, typedLog, typedCalled)
	}
	if rows.Next() || rows.Err() != nil {
		s.T.Fatal("state exhaustion", rows.Err())
	}
	if err := rows.Close(); err != nil {
		s.T.Fatal(err)
	}
	s.Record.Mu.Lock()
	defer s.Record.Mu.Unlock()
	var op *Operation
	for _, candidate := range s.Record.Operations {
		if candidate.SQL == q && candidate.Err == nil && candidate.Columns != nil {
			op = candidate
		}
	}
	if op == nil || !op.Closed || op.CloseErr != nil || !reflect.DeepEqual(op.Columns, columns) || !reflect.DeepEqual(op.Rows, [][]driver.Value{{last, n, called}}) {
		s.T.Fatal("recorded state/closure", op)
	}
	return n
}

func (s *TempReferenceSession) Schemas(names ...string) {
	s.T.Helper()
	// Registered after connection cleanup: run while the peer still owns its physical session.
	s.T.Cleanup(func() {
		for _, name := range names {
			if _, err := s.Peer.ExecContext(s.P.Ctx, "DROP SCHEMA "+name+" CASCADE"); err != nil {
				s.T.Error(err)
			}
		}
	})
}

func (s *TempReferenceSession) Retire() {
	s.T.Helper()
	var pid int
	if s.Postgres {
		pid = SequenceOwnedLockPID(s.T, s.P.Ctx, s.C)
	}
	s.DB.SetMaxIdleConns(0)
	if err := s.C.Close(); err != nil {
		s.T.Fatal(err)
	}
	if !s.Postgres {
		return
	}
	ctx, cancel := context.WithTimeout(s.P.Ctx, 2*time.Second)
	defer cancel()
	for {
		var live bool
		if err := s.Peer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
			s.T.Fatal("observing TEMP owner exit", err)
		}
		if !live {
			return
		}
	}
}

func (s *TempReferenceSession) Snapshot(rows *sql.Rows, q string) {
	s.T.Helper()
	frozen := SequenceFrozenRows{Executor: s.C, Rows: rows, Query: q}
	step := Step{Query: q, SelectRows: true, Columns: []string{"last_value", "is_called"}, Types: []string{"INT8", "BOOL"}, Rows: [][]any{{int64(35), true}}}
	step.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
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
	RunStep(s.T, s.P.Ctx, frozen, s.Record, step)
}
