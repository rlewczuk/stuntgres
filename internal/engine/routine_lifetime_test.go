package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/role"
)

func TestRoutineDirectStrictNullLifetime(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	readerOp, readerDone := roleOperation(t, e, "jsonb_authority_reader")
	defer readerDone()
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "abs(NULL::integer)")
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil || !exec.StrictScalarRoutine(captured) {
		t.Fatal("missing actual strict scalar carrier", err)
	}
	original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	for _, dependency := range original {
		if dependency.Ref.Address == captured.Address() {
			if err := e.SetObjectACL(context.Background(), dependency.Ref, authorization.ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Use the actual statement-host source lane, not the helper's hard recipe.
	evaluation.RoutineAuthority = nil
	evaluation.RoutineRecipe = false
	evaluation.RoutineChecks = nil
	scope.routineAuth = *eMustJSONBRoutineAuthority(t, e, readerOp)
	scope.routineDependencies = original
	if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
		t.Fatal("strict NULL acquired EXECUTE demand", err)
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	target := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("jsonb_authority_owner")
	if err := role.StageOwnership(stage, base.MustProcedure(captured.Address()), target); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	e.DiscardCatalogStage(stage)
	if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
		t.Fatal("owner COW changed retained source authority snapshot", err)
	}
	scope.routineAuth = *eMustJSONBRoutineAuthority(t, e, readerOp)
	err = exec.AdmitRoutineCall(&evaluation, node, false, false)
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal("strict NULL rescued stale direct graph", err)
	}
	if base.Get(captured.Address()).Revision() != captured.Revision() || captured.Owner == target {
		t.Fatal("owner COW mutated original declaration")
	}
}
