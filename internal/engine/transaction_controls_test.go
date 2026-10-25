package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

func controlsExecute(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, statement(t, q), 0, nil)
	if r.Rows != nil {
		r.Rows.Close()
	}
	if err == nil && r.Count != 0 {
		t.Fatal(q, r.Count)
	}
	return err
}

func controlsRequire(t *testing.T, s *session.Session, q string) {
	t.Helper()
	if err := controlsExecute(t, s, q); err != nil {
		t.Fatal(q, err)
	}
}
