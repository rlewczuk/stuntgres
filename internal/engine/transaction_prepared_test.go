package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func preparedUnitControl(t *testing.T, e *core.Engine, s *session.Session, kind parser.TransactionControlKind, gid string) error {
	t.Helper()
	op := transactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scope.Execute(&parser.TransactionControl{Kind: kind, GID: gid}, 0, nil)
	finish := scope.Finish(err)
	if finish != err || scope.Finish(nil) != err {
		t.Fatal("control Finish replaced its execution diagnostic", err, finish)
	}
	return finish
}
