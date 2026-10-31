package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

func statsFunctionKey(t *testing.T, e *core.Engine, name string) statistics.Key {
	t.Helper()
	read := e.CatalogRead()
	for _, descriptor := range read.Descriptors() {
		if d, ok := descriptor.(*catalog.Procedure); ok && d.Name() == name {
			return statistics.Key{Object: read.Owner().Domain.Ref(read.Epoch(), d.Address()), Category: statistics.Function}
		}
	}
	t.Fatalf("missing actual procedure %s", name)
	return statistics.Key{}
}

// Consume real command results before releasing the SQL lease. Publication is
// ordinary idle/terminal publication, never a test-only writer flush.
func statsFunctionSQL(t *testing.T, s *session.Session, q string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		return err
	}
	defer op.Done()
	r, err := core.EngineOf(s).Execute(op, enginetest.Statement(t, q), 0, nil)
	if err != nil {
		return err
	}
	if r.Rows != nil {
		enginetest.Tuples(r)
		return r.Rows.Close()
	}
	return nil
}

func statsFunctionExec(t *testing.T, s *session.Session, queries ...string) {
	t.Helper()
	for _, q := range queries {
		if err := statsFunctionSQL(t, s, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func statsFunctionRetire(t *testing.T, s *session.Session) {
	t.Helper()
	s.RequestEnd(session.CleanClose)
	if err := s.WaitEnd(context.Background()); err != nil {
		t.Fatal(err)
	}
}
