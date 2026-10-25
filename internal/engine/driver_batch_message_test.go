package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

type batchUnitMessage struct {
	scope  *StatementScope
	op     *session.Operation
	ended  bool
	result error
}

func (m *batchUnitMessage) step(stmt parser.Statement) (Result, error) {
	if m.scope.Finished {
		next, err := m.scope.NextStatement()
		if err != nil {
			return Result{}, err
		}
		m.scope = next
	}
	result, err := m.scope.Execute(stmt, 0, nil)
	finish := m.scope.Finish(err)
	if err == nil {
		err = finish
	}
	return result, err
}

func (m *batchUnitMessage) end(err error) error {
	if !m.ended {
		m.result = m.scope.FinishMessage(err)
		m.ended = true
		m.op.Done()
	}
	return m.result
}

func batchUnitStart(t *testing.T, s *session.Session, ctx context.Context) *batchUnitMessage {
	t.Helper()
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := EngineOf(s).StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err = scope.RetainMessage(); err != nil {
		_ = scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	return &batchUnitMessage{scope: scope, op: op}
}
