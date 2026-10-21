package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

type domainPhaseList []domainCheckPhase

func (domainPhaseList) DomainCheckPhasesSlot() {}

// programPhases returns the programme's phases. The list shares its backing
// array with the slot, so an indexed update is seen through the programme.
func programPhases(p exec.DomainCheckProgram) domainPhaseList {
	phases, _ := p.Phases.(domainPhaseList)
	return phases
}

type domainCheckPhase struct {
	number    ids.CommandIdentity
	actor     authorization.PrincipalRef
	ri        bool
	admission routineAdmissionProgram
}

func (s *StatementScope) readmitPreparedPrograms(view AuthorizationView) error {
	if len(s.domains.preparedChecks) == 0 && len(s.routinePrograms) == 0 && len(s.indexBindings) == 0 {
		return nil
	}
	auth := routineAccessOf(view)
	for _, programme := range s.domains.preparedChecks {
		phases := programPhases(programme)
		for i := range phases {
			if err := phases[i].admission.readmit(auth); err != nil {
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
func (p *routineAdmissionProgram) initializePhase(original *routineAdmissionProgram, auth exec.RoutineAccess) error {
	for _, call := range original.calls {
		dependency := [1]catalog.ExpressionDependency{{Ref: call.reference, Revision: call.routine.Revision()}}
		if err := admitRoutineDescriptor(auth, dependency[:], call.routine, call.consumer); err != nil {
			return call.body.Annotate(err)
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
