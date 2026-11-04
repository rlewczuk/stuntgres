package core

import (
	"fmt"
	"math"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ObserveSepgsql consumes actual-phase evidence on its genuine command owner.
// A later balanced return/setcon does not reinterpret that historical decision.
func (s *StatementScope) ObserveSepgsql(observation access.SepgsqlObservation) error {
	if s == nil || s.Op == nil || s.Finished || s.Engine().Sepgsql != observation.Policy || observation.Policy == nil {
		return fmt.Errorf("stuntgres: foreign simulated MAC observation owner")
	}
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	owner := &s.Engine().Catalog.Owner
	if observation.Reference != owner.Domain.Ref(observation.Reference.Epoch, observation.Reference.Address) || observation.Reference.Epoch != s.TxRef.Epoch() {
		return ids.ErrCatalogReplaced
	}
	if s.Op.Session.Sepgsql == nil {
		return fmt.Errorf("stuntgres: missing actual simulated MAC subject")
	}
	subject, generation, frame, depth := s.Op.Session.Sepgsql.PhaseSnapshot()
	if subject != observation.Subject || generation != observation.Generation || frame != observation.Frame || depth != observation.Depth {
		return fmt.Errorf("stuntgres: stale simulated MAC decision phase")
	}
	for _, previous := range s.sepgsqlObservations {
		if previous == observation {
			return nil
		}
	}
	s.sepgsqlObservations = append(s.sepgsqlObservations, observation)
	return nil
}
func (s *StatementScope) MarkSepgsqlObservations() access.SepgsqlObservationMark {
	return access.SepgsqlObservationMark{Observations: len(s.sepgsqlObservations), Initializations: s.routineInitializationCompletion, Retirements: s.retirementCompletion}
}
func (s *StatementScope) RestoreSepgsqlObservations(mark access.SepgsqlObservationMark) {
	if mark.Observations < 0 || mark.Observations > len(s.sepgsqlObservations) || mark.Initializations > s.routineInitializationCompletion || mark.Retirements > s.retirementCompletion {
		panic("stuntgres: foreign simulated MAC observation mark")
	}
	clear(s.sepgsqlObservations[mark.Observations:])
	s.sepgsqlObservations = s.sepgsqlObservations[:mark.Observations]
	for call, receipt := range s.routineInitializations {
		if receipt.completion > mark.Initializations {
			delete(s.routineInitializations, call)
		}
	}
	for call, completion := range s.directRoutineInitializations {
		if completion > mark.Initializations {
			delete(s.directRoutineInitializations, call)
		}
	}
	for address, receipt := range s.sepgsqlRetirements {
		if receipt.completion > mark.Retirements {
			delete(s.sepgsqlRetirements, address)
		}
	}
}
func (s *StatementScope) completeSepgsqlRetirement() (uint64, error) {
	if s.retirementCompletion == math.MaxUint64 {
		return 0, fmt.Errorf("stuntgres: simulated MAC retirement completion exhausted")
	}
	s.retirementCompletion++
	return s.retirementCompletion, nil
}
func (s *StatementScope) completeRoutineInitialization() (uint64, error) {
	if s.routineInitializationCompletion == math.MaxUint64 {
		return 0, fmt.Errorf("stuntgres: routine initialization completion exhausted")
	}
	s.routineInitializationCompletion++
	return s.routineInitializationCompletion, nil
}
func (s *StatementScope) validateSepgsqlObservations(current *catalog.State) error {
	if len(s.sepgsqlObservations) == 0 {
		return nil
	}
	if s.Op == nil || current == nil || s.Engine().Sepgsql == nil {
		return ids.ErrCatalogReplaced
	}
	if err := s.Op.Context().Err(); err != nil {
		return err
	}
	for _, observation := range s.sepgsqlObservations {
		if observation.Policy != s.Engine().Sepgsql || observation.Reference != s.Engine().Catalog.Domain.Ref(current.Epoch(), observation.Reference.Address) || current.Get(observation.Reference.Address) != observation.ExpectedObject || current.SecurityLabel(observation.Key) != observation.ExpectedLabel {
			return catalog.Conflict{}
		}
	}
	return nil
}

// Own successful publication advances only the predecessor fence. Historical
// subject/actual-record evidence is retained unchanged; no permission is replayed.
func (s *StatementScope) advanceSepgsqlPublication(before, next *catalog.State) {
	if s == nil {
		return
	}
	for i := range s.sepgsqlObservations {
		observation := &s.sepgsqlObservations[i]
		if observation.ExpectedObject == before.Get(observation.Reference.Address) && observation.ExpectedLabel == before.SecurityLabel(observation.Key) {
			observation.ExpectedObject = next.Get(observation.Reference.Address)
			observation.ExpectedLabel = next.SecurityLabel(observation.Key)
		}
	}
}
func (s *StatementScope) captureSepgsqlObservations() error {
	if len(s.sepgsqlObservations) == 0 || len(s.Stage.Rows) == 0 && len(s.Stage.Deletes) == 0 && s.Catalog == nil {
		return nil
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := s.validateSepgsqlObservations(e.Catalog.State); err != nil {
		return err
	}
	journal := s.PendingCatalog().Clone()
	for _, observation := range s.sepgsqlObservations {
		if err := journal.ObserveSecurityLabelPredecessor(e.Catalog.State, observation.Key, observation.ExpectedLabel, observation.ExpectedObject); err != nil {
			return err
		}
	}
	if err := s.AcquireJournalRoleDependenciesLocked(journal); err != nil {
		return err
	}
	if err := s.validateSepgsqlObservations(e.Catalog.State); err != nil {
		return err
	}
	s.Catalog = journal
	return nil
}
