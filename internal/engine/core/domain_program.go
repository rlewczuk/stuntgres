package core

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
)

type domainPhaseList []DomainCheckPhase

func (domainPhaseList) DomainCheckPhasesSlot() {}

func (phases domainPhaseList) RoutineInitialization(number ids.CommandIdentity, actor authorization.PrincipalRef, ri bool, ordinal int) (exec.RoutineInitialization, bool) {
	for _, phase := range phases {
		if phase.Number == number && phase.Actor == actor && phase.Ri == ri && ordinal >= 0 && ordinal < len(phase.Initializations) {
			return phase.Initializations[ordinal], true
		}
	}
	return exec.RoutineInitialization{}, false
}

// ProgramPhases returns the programme's phases. The list shares its backing
// array with the slot, so an indexed update is seen through the programme.
func ProgramPhases(p exec.DomainCheckProgram) domainPhaseList {
	phases, _ := p.Phases.(domainPhaseList)
	return phases
}

type DomainCheckPhase struct {
	Number          ids.CommandIdentity
	Actor           authorization.PrincipalRef
	Ri              bool
	Admission       exec.RoutineProgram
	Initializations []exec.RoutineInitialization
}

func (s *StatementScope) readmitPreparedPrograms(view AuthorizationView) error {
	if len(s.sepgsqlObservations) != 0 {
		if err := s.validateSepgsqlObservations(view.Sepgsql.Predecessor); err != nil {
			return err
		}
	}
	if len(s.Domains.PreparedChecks) == 0 && len(s.routinePrograms) == 0 && len(s.IndexBindings) == 0 {
		return nil
	}
	auth := RoutineAccessOf(view)
	for _, programme := range s.Domains.PreparedChecks {
		phases := ProgramPhases(programme)
		for i := range phases {
			if err := phases[i].Admission.Readmit(auth, exec.ConsumedGraphRevalidation); err != nil {
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
