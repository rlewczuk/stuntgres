package engine

import (
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func domainUnitBindSelect(t *testing.T, e *core.Engine, op *session.Operation, q string) *plan.Query {
	t.Helper()
	raw := statement(t, q).(*parser.Select)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	b := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.Query.Auth = &auth
	var query *plan.Query
	if err == nil {
		query, err = b.BindSelect(raw, false)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(q, err)
	}
	return query
}
