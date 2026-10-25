package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Exercise the private binder/stager directly, not SQL execution wrappers.
func materializedUnitStage(t *testing.T, e *core.Engine) (*session.Operation, func(), *core.CatalogStage, *catalog.Relation) {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	statements, err := parser.Parse("CREATE MATERIALIZED VIEW public.unit_mv AS SELECT 1::int AS i WITH NO DATA")
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*parser.CreateQueryTable)
	input, _ := op.Session.Settings.Presentation()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := core.Describe(context.Background(), e.AnalysisHost(op), s, auth, ids.ObjectRef{}, input, nil, nil)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage, descriptor, skipped, err := core.EnginePrepareQueryTableLocked(e, op, auth, s, b)
	e.Mu.Unlock()
	if err != nil || skipped {
		t.Fatalf("private stage: %v, skipped %v", err, skipped)
	}
	return op, done, stage, descriptor
}
