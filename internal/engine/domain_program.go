package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

type domainPhaseList []DomainCheckPhase

func (domainPhaseList) DomainCheckPhasesSlot() {}

// ProgramPhases returns the programme's phases. The list shares its backing
// array with the slot, so an indexed update is seen through the programme.
func ProgramPhases(p exec.DomainCheckProgram) domainPhaseList {
	phases, _ := p.Phases.(domainPhaseList)
	return phases
}

type DomainCheckPhase struct {
	Number    ids.CommandIdentity
	Actor     authorization.PrincipalRef
	Ri        bool
	Admission exec.RoutineProgram
}

func (s *StatementScope) readmitPreparedPrograms(view AuthorizationView) error {
	if len(s.Domains.PreparedChecks) == 0 && len(s.routinePrograms) == 0 && len(s.IndexBindings) == 0 {
		return nil
	}
	auth := RoutineAccessOf(view)
	for _, programme := range s.Domains.PreparedChecks {
		phases := ProgramPhases(programme)
		for i := range phases {
			if err := phases[i].Admission.Readmit(auth); err != nil {
				return err
			}
		}
	}
	if err := s.readmitRoutinePrograms(auth); err != nil {
		return err
	}
	for _, binding := range s.IndexBindings {
		if err := binding.ReadmitPrograms(auth); err != nil {
			return err
		}
	}
	return nil
}
