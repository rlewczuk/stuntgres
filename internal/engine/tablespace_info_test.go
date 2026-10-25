package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
)

func tablespaceInfoUnitScope(t *testing.T, e *core.Engine) *core.StatementScope {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(context.Canceled) })
	e.Mu.Lock()
	err = e.CaptureCommentSnapshotLocked(scope)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
