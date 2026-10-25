package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
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

func controlsDescribe(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	d, err := core.EngineOf(s).Describe(op, statement(t, q))
	if err == nil && (d.Parameters.Count != 0 || len(d.Results) != 0) {
		t.Fatal(d)
	}
	return err
}

func controlsRequire(t *testing.T, s *session.Session, q string) {
	t.Helper()
	if err := controlsExecute(t, s, q); err != nil {
		t.Fatal(q, err)
	}
}

func controlsAbort(t *testing.T, s *session.Session) {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	_, err := core.EngineOf(s).Execute(op, statement(t, "INSERT INTO t VALUES(NULL)"), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23502" {
		t.Fatal(err)
	}
}
