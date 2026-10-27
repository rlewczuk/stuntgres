package exec

import "stuntgres/internal/engine/plan"

// CapturePlannerAdmission must precede source/host rebinding for retained streams.
func (s *State) CapturePlannerAdmission() {
	if s.methodsCaptured {
		return
	}
	s.methodsCaptured = true
	if s.Source != nil {
		if host := s.Source.LiveHost(); host != nil {
			s.Methods = host.AdmittedPlannerMethods()
		}
	}
}

func sourcePlannerMethods(source *Source) plan.PlannerMethods {
	if source != nil {
		if host := source.LiveHost(); host != nil {
			return host.AdmittedPlannerMethods()
		}
	}
	return plan.PlannerMethods{}
}
