package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

func conversionUnitAuth(t *testing.T, e *core.Engine) (*session.Operation, core.AuthorizationView) {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return op, auth
}
