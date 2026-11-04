package core

import (
	"errors"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/sqlstate"
)

// All SQL query/cursor evaluation borrowing exec.Authority is serialized by the
// existing session.TransactionOwner.Admission lease through StatementScope.Finish (and
// message End). Query pipelines do not dispatch parallel expression workers.
// Thus first-body initialization has that existing single owner, including lazy
// sources/retained cursors; no metadata mutex, sync.Once or new cell is needed.
// Each eager outer occurrence still validates its own original recipe/lifetime.
// A nonnil outer keys lazy inquiry reuse. A nil outer is an unkeyed SQL
// EXECUTE-actual programme; its individual calls still carry exact identities.
// Both are consumed graphs owned by the same existing statement collection.
type commandRoutineProgram struct {
	outer          *catalog.Procedure
	consumer       *catalog.Relation
	ri             bool
	admission      *exec.RoutineProgram
	riQuery        string // Action diagnostic after its live owner phase has unwound.
	subject        string
	policy         *sepgsql.Runtime
	labels         catalog.SecurityLabelRoot
	initialization exec.RoutineInitialization
}

func admitInquiryBody(c *exec.Context, node expr.Info) (err error) {
	scope := ScopeOf(c.LiveHost())
	if scope == nil {
		return nil
	} // Legitimate standalone internal provider fixtures.
	mark := scope.MarkSepgsqlObservations()
	defer func() {
		if err != nil {
			scope.RestoreSepgsqlObservations(mark)
		}
	}()
	local := *c
	recipe, err := exec.ExpressionRecipeDescriptor(node)
	if err != nil {
		return err
	}
	if recipe != nil && (!local.RoutineGraft || local.RoutineGraftSource != recipe) || recipe == nil && local.RoutineGraft {
		if err = exec.SelectExpressionRecipe(&local, recipe); err != nil {
			return err
		}
	}
	outer, err := exec.ExpressionRoutineDescriptor(node)
	if err != nil || outer == nil {
		return err
	}
	auth, dependencies, err := exec.RoutineInvocationAuthority(&local)
	if err != nil || !auth.Live() {
		return err
	}
	caller := local
	local, err = exec.BeginNativeBody(&local, outer, node)
	if err != nil {
		return err
	}
	ri := scope.ForeignActionAuth != nil && scope.ForeignActionAuth.State.CurrentUser == auth.State.CurrentUser
	for _, programme := range scope.routinePrograms {
		if programme.outer == outer && programme.consumer == local.RoutineConsumer &&
			programme.initialization == local.RoutineInitialization &&
			programme.ri == ri && programme.subject == auth.Sepgsql.Subject && programme.policy == auth.Sepgsql.Policy && programme.labels == auth.Catalog.State().SecurityLabelRoot() && len(programme.admission.Calls) != 0 &&
			programme.admission.Calls[0].Actor == auth.State.CurrentUser {
			return nil
		}
	}
	programme := commandRoutineProgram{outer: outer, consumer: local.RoutineConsumer, ri: ri, admission: new(exec.RoutineProgram), subject: auth.Sepgsql.Subject, policy: auth.Sepgsql.Policy, labels: auth.Catalog.State().SecurityLabelRoot(), initialization: local.RoutineInitialization}
	local.RoutineProgram = programme.admission
	// The outer call has already passed executable-graph admission. Retain
	// that actual first entry too, so later readmission preserves outer-before-
	// body diagnostics without another permission check at each row.
	// Record the outer in its admitted caller phase, before using the derived
	// body's immediate-child lineage. The outer is not its own body child.
	if err := programme.admission.Record(&caller, dependencies, outer); err != nil {
		return err
	}
	local.RoutineBodyContext = exec.RoutineBodyContext{Outer: outer, Statement: true}
	for ordinal, edge := range outer.Body {
		body, err := exec.BodyRoutineDescriptor(&local, auth, dependencies, edge)
		if err == nil {
			err = exec.AdmitInitializedBodyRoutine(&local, auth, dependencies, body, uint32(ordinal+1))
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

func (s *StatementScope) readmitRoutinePrograms(auth exec.RoutineAccess) error {
	for i := range s.routinePrograms {
		program := &s.routinePrograms[i]
		if err := program.admission.Readmit(auth, exec.ConsumedGraphRevalidation); err != nil {
			// A live action owns its existing foreignActionError frame.
			// Final publication runs after that phase has restored the caller.
			var diagnostic *sqlstate.Error
			if program.riQuery != "" && s.ForeignActionAuth == nil && errors.As(err, &diagnostic) {
				copy := *diagnostic
				if copy.Context != "" {
					copy.Context += "\n"
				}
				copy.Context += `SQL statement "` + program.riQuery + `"`
				return sqlstate.WithDiagnostic(err, &copy)
			}
			return err
		}
	}
	return nil
}
