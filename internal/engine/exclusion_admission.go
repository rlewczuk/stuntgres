package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func (s *StatementScope) BeginExclusionProjectionLocked(e *Engine) {
	if s.IndexesOrZero().ExclusionStarted {
		return
	}
	s.indexes().ExclusionStarted = true
	s.indexes().ExclusionModes = ownerExtensionOrZero(s.Owner).exclusionModes
}
func (s *StatementScope) InstallExclusionProjectionLocked(e *Engine, p *index.ExclusionProjection) {
	e.Exclusions.InstallLocked(s.LockTicket, p)
}
func (s *StatementScope) exclusionCandidateLocked(e *Engine, proposal *index.WriteCandidate, excluded []storage.RowVersion, catalog *catalog.State) (storage.RowVersion, error) {
	if proposal.Exclusion != nil {
		row := *proposal.Exclusion
		row.IndexAdmission = catalog
		return row, nil
	}
	if e.Storage.VersionFrontier() == ^uint64(0) || len(excluded) == 0 && e.Storage.RowFrontier() == ^uint64(0) {
		return storage.RowVersion{}, fmt.Errorf("stuntgres: row identity exhausted")
	}
	row := storage.RowVersion{Version: e.Storage.AllocateVersionLocked(), Values: proposal.Values, IndexAdmission: catalog}
	if len(excluded) == 0 {
		row.Row = e.Storage.AllocateRowLocked()
	} else {
		predecessor := excluded[len(excluded)-1]
		row.Row, row.Base = predecessor.Row, predecessor.Base
		if row.Base == 0 && predecessor.Born != 0 {
			row.Base = predecessor.Version
		}
		row.KeyChangedAt, row.PendingKeyChange = predecessor.KeyChangedAt, predecessor.PendingKeyChange
	}
	return row, nil
}
func (s *StatementScope) projectExclusionDeletionLocked(e *Engine, candidate visibility.DeleteCandidate) {
	parent, ok := e.PrivateCatalogLocked(s.Owner.Catalog).Relation(candidate.Ref.Address)
	if !ok || len(parent.Constraints.Exclusion) == 0 {
		return
	}
	s.BeginExclusionProjectionLocked(e)
	p := e.Exclusions.ProjectionLocked(s.LockTicket)
	p.EndRow(candidate.Ref.Address, parent.Constraints.Exclusion, candidate.Row, candidate.Version)
	s.InstallExclusionProjectionLocked(e, p)
}
