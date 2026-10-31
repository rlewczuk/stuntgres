package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineDirectStrictNullLifetime(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	readerOp, readerDone := enginetest.RoleOperation(t, e, "jsonb_authority_reader")
	defer readerDone()
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "abs(NULL::integer)")
	captured, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || captured == nil || !exec.StrictScalarRoutine(captured) {
		t.Fatal("missing actual strict scalar carrier", err)
	}
	original := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	for _, dependency := range original {
		if dependency.Ref.Address == captured.Address() {
			if err := enginetest.SetObjectACL(e, context.Background(), dependency.Ref, authorization.ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Use the actual statement-host source lane, not the helper's hard recipe.
	evaluation.RoutineAuthority = nil
	evaluation.RoutineRecipe = false
	evaluation.RoutineChecks = nil
	scope.RoutineAuth = *eMustJSONBRoutineAuthority(t, e, readerOp)
	scope.RoutineDependencies = original
	if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
		t.Fatal("strict NULL acquired EXECUTE demand", err)
	}
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	target := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("jsonb_authority_owner")
	if err := role.StageOwnership(stage, base.MustProcedure(captured.Address()), target); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	e.DiscardCatalogStage(stage)
	if err := exec.AdmitRoutineCall(&evaluation, node, false, false); err != nil {
		t.Fatal("owner COW changed retained source authority snapshot", err)
	}
	scope.RoutineAuth = *eMustJSONBRoutineAuthority(t, e, readerOp)
	err = exec.AdmitRoutineCall(&evaluation, node, false, false)
	var conflict catalog.Conflict
	if !errors.As(err, &conflict) {
		t.Fatal("strict NULL rescued stale direct graph", err)
	}
	if base.Get(captured.Address()).Revision() != captured.Revision() || captured.Owner == target {
		t.Fatal("owner COW mutated original declaration")
	}
}

func TestRoutineBoundStrictNullAdmission(t *testing.T) {
	e, op, scope := jsonbRecordRoutineUnitFixture(t)
	reader, done := enginetest.RoleOperation(t, e, "jsonb_authority_reader")
	defer done()
	for _, text := range []string{"abs($1::integer)", "abs(v)"} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
		captured, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || !exec.StrictScalarRoutine(captured) {
			t.Fatal("missing strict scalar declaration", err)
		}
		for _, dependency := range binder.Dependencies {
			if dependency.Ref.Address == captured.Address() {
				if err := enginetest.SetObjectACL(e, context.Background(), dependency.Ref, authorization.ACLValue{Explicit: true}); err != nil {
					t.Fatal(err)
				}
			}
		}
		evaluation.RoutineAuthority = nil
		evaluation.RoutineRecipe = false
		evaluation.RoutineChecks = nil
		scope.RoutineAuth = *eMustJSONBRoutineAuthority(t, e, reader)
		scope.RoutineDependencies = binder.Dependencies
		integer := enginetest.Usage(e, sqltype.Int4).Type
		evaluation.Parameters = []sqltype.Value{sqltype.Null(integer)}
		err = exec.AdmitRoutineCall(&evaluation, node, false, false)
		if text == "abs(v)" {
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function abs"})
			continue
		}
		if err != nil {
			t.Fatal("known bound NULL acquired EXECUTE demand", err)
		}
		// Generic planning and non-NULL custom plans still invoke the routine.
		evaluation.PreserveParameterDemand = true
		err = exec.AdmitRoutineCall(&evaluation, node, false, false)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function abs"})
		evaluation.PreserveParameterDemand = false
		one, err := sqltype.Integer(integer, 1)
		if err != nil {
			t.Fatal(err)
		}
		evaluation.Parameters[0] = one
		err = exec.AdmitRoutineCall(&evaluation, node, false, false)
		nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "42501", Message: "permission denied for function abs"})
	}
}
