package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

type domainCheckIdentity struct {
	transaction ids.TxRef
	command     ids.CommandID
}

// The existing domain map owns both prepared expressions and every actual phase
// initialization. These are consumed call graphs with original actors/lifetimes,
// not cached permission results. No CHECK truth is recorded or replayed.
type domainCheckProgram struct {
	expressions []expr.Expr
	phases      []domainCheckPhase
}
type domainCheckPhase struct {
	number    domainCheckIdentity
	actor     authorization.PrincipalRef
	ri        bool
	admission routineAdmissionProgram
}

func (s *StatementScope) readmitPreparedPrograms(auth AuthorizationView) error {
	for _, programme := range s.domainPreparedChecks {
		for i := range programme.phases {
			if err := programme.phases[i].admission.readmit(auth); err != nil {
				return err
			}
		}
	}
	if err := s.readmitRoutinePrograms(auth); err != nil {
		return err
	}
	for _, binding := range s.indexBindings {
		if err := binding.readmitPrograms(auth); err != nil {
			return err
		}
	}
	return nil
}

// A new trusted caller phase may borrow phase-independent cooked values only
// after admitting the exact original consumed graph under its own actual actor.
func (p *routineAdmissionProgram) initializePhase(original *routineAdmissionProgram, auth AuthorizationView) error {
	for _, call := range original.calls {
		dependency := [1]catalog.ExpressionDependency{{Ref: call.reference, Revision: call.routine.Revision()}}
		if err := admitRoutineDescriptor(auth, dependency[:], call.routine, call.consumer); err != nil {
			return call.body.annotate(err)
		}
		call.actor = auth.State.CurrentUser
		p.calls = append(p.calls, call)
	}
	return nil
}

func (p *routineAdmissionProgram) include(other *routineAdmissionProgram) {
	for _, call := range other.calls {
		found := false
		for _, old := range p.calls {
			if old.reference == call.reference && old.consumer == call.consumer && old.actor == call.actor {
				found = true
				break
			}
		}
		if !found {
			p.calls = append(p.calls, call)
		}
	}
}
