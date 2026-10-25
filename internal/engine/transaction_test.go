package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func transactionOperation(t *testing.T, s *session.Session) *session.Operation {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}

func transactionBegin(t *testing.T, s *session.Session) ids.TxRef {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	ref, err := core.EngineOf(s).Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func transactionEnd(t *testing.T, s *session.Session, ref ids.TxRef, commit bool) core.EndResult {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	var r core.EndResult
	var err error
	if commit {
		r, err = core.EngineOf(s).Commit(op, ref)
	} else {
		r, err = core.EngineOf(s).Rollback(op, ref)
	}
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func transactionRows(t *testing.T, s *session.Session, q string, want ...int64) *exec.FinalRows {
	t.Helper()
	r := sessionSQL(t, s, q)
	t.Cleanup(func() {
		if err := r.Rows.Close(); err != nil {
			t.Error(err)
		}
	})
	got := tuples(r)
	expected := make([][]driver.Value, len(want))
	for i, v := range want {
		expected[i] = []driver.Value{v}
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("%s got %v want %v", q, got, expected)
	}
	return r.Rows.(*exec.FinalRows)
}
