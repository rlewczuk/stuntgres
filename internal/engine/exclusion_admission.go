package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func (s *StatementScope) beginExclusionProjectionLocked(e *Engine) {
	if s.indexesOrZero().ExclusionStarted {
		return
	}
	s.indexes().ExclusionStarted = true
	s.indexes().ExclusionModes = ownerExtensionOrZero(s.owner).exclusionModes
}
func (s *StatementScope) installExclusionProjectionLocked(e *Engine, p *index.ExclusionProjection) {
	e.exclusions.InstallLocked(s.ticket, p)
}
func (s *StatementScope) exclusionCandidateLocked(e *Engine, proposal *index.WriteCandidate, excluded []storage.RowVersion, catalog *catalog.State) (storage.RowVersion, error) {
	if proposal.Exclusion != nil {
		row := *proposal.Exclusion
		row.IndexAdmission = catalog
		return row, nil
	}
	if e.storage.VersionFrontier() == ^uint64(0) || len(excluded) == 0 && e.storage.RowFrontier() == ^uint64(0) {
		return storage.RowVersion{}, fmt.Errorf("stuntgres: row identity exhausted")
	}
	row := storage.RowVersion{Version: e.storage.AllocateVersionLocked(), Values: proposal.Values, IndexAdmission: catalog}
	if len(excluded) == 0 {
		row.Row = e.storage.AllocateRowLocked()
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
	parent, ok := e.privateCatalogLocked(s.owner.Catalog).Relation(candidate.Ref.Address)
	if !ok || len(parent.Constraints.Exclusion) == 0 {
		return
	}
	s.beginExclusionProjectionLocked(e)
	p := e.exclusions.ProjectionLocked(s.ticket)
	p.EndRow(candidate.Ref.Address, parent.Constraints.Exclusion, candidate.Row, candidate.Version)
	s.installExclusionProjectionLocked(e, p)
}
