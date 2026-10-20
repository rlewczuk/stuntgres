package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/routine"
)

// All SQL query/cursor evaluation borrowing exec.Authority is serialized by the
// existing transactionOwner.admission lease through StatementScope.Finish (and
// message End). Query pipelines do not dispatch parallel expression workers.
// Thus first-body initialization has that existing single owner, including lazy
// sources/retained cursors; no metadata mutex, sync.Once or new cell is needed.
// Each eager outer occurrence still validates its own original recipe/lifetime.
// A nonnil outer keys lazy inquiry reuse. A nil outer is an unkeyed SQL
// EXECUTE-actual programme; its individual calls still carry exact identities.
// Both are consumed graphs owned by the same existing statement collection.
type commandRoutineProgram struct {
	outer     *catalog.Procedure
	consumer  *catalog.Relation
	ri        bool
	admission routineAdmissionProgram
}

func admitInquiryBody(c *exec.Context, node expr.Info) error {
	scope := scopeOf(c.LiveHost())
	if scope == nil {
		return nil
	} // Legitimate standalone internal provider fixtures.
	local := *c
	recipe, err := routine.ExpressionRecipeDescriptor(node)
	if err != nil {
		return err
	}
	if recipe != nil && (!local.RoutineGraft || local.RoutineConsumer != recipe) || recipe == nil && local.RoutineGraft {
		if err = selectExpressionRecipe(&local, recipe); err != nil {
			return err
		}
	}
	outer, err := routine.ExpressionRoutineDescriptor(node)
	if err != nil || outer == nil {
		return err
	}
	auth, dependencies, err := routineAdmissionAuthority(&local)
	if err != nil || auth.engine == nil {
		return err
	}
	ri := scope.foreignActionAuth != nil && scope.foreignActionAuth.State.CurrentUser == auth.State.CurrentUser
	for _, programme := range scope.routinePrograms {
		if programme.outer == outer && programme.consumer == local.RoutineConsumer &&
			programme.ri == ri && len(programme.admission.calls) != 0 &&
			programme.admission.calls[0].actor == auth.State.CurrentUser {
			return nil
		}
	}
	programme := commandRoutineProgram{outer: outer, consumer: local.RoutineConsumer, ri: ri}
	local.RoutineProgram = &programme.admission
	// The outer call has already passed executable-graph admission. Retain
	// that actual first entry too, so later readmission preserves outer-before-
	// body diagnostics without another permission check at each row.
	if err := programme.admission.record(&local, dependencies, outer); err != nil {
		return err
	}
	local.RoutineBodyContext = exec.RoutineBodyContext{Outer: outer, Statement: true}
	for _, edge := range outer.Body {
		body, err := bodyRoutineDescriptor(&local, auth, dependencies, edge)
		if err == nil {
			err = admitRecipeRoutineDescriptor(&local, auth, dependencies, body)
		}
		if err != nil {
			return local.RoutineBodyContext.Annotate(err)
		}
	}
	// Publish only complete successful initialization. The footprint is an
	// original-call graph, not granted rights; serial recapture readmits it.
	scope.routinePrograms = append(scope.routinePrograms, programme)
	return nil
}

func (s *StatementScope) readmitRoutinePrograms(auth AuthorizationView) error {
	for i := range s.routinePrograms {
		if err := s.routinePrograms[i].admission.readmit(auth); err != nil {
			return err
		}
	}
	return nil
}
