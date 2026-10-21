package engine

import (
	"stuntgres/internal/engine/authorization"
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
	admission exec.RoutineProgram
}

func (s *StatementScope) readmitPreparedPrograms(view AuthorizationView) error {
	if len(s.domains.preparedChecks) == 0 && len(s.routinePrograms) == 0 && len(s.indexBindings) == 0 {
		return nil
	}
	auth := routineAccessOf(view)
	for _, programme := range s.domains.preparedChecks {
		phases := programPhases(programme)
		for i := range phases {
			if err := phases[i].admission.Readmit(auth); err != nil {
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
