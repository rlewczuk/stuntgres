package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
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
func (s *StatementScope) publishExclusionCandidate(ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, proposal *index.WriteCandidate, excluded []storage.RowVersion, precheck bool, purpose index.ProbePurpose) (storage.RowVersion, error) {
	e := s.engine()
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: e.catalog.read(binding.Catalog)}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	if binding.TypeName != nil {
		evaluation.Input.TypeDiagnosticName = binding.TypeName
	}
	definition := index.ExclusionDefinition(binding.Parent, bound.Index)
	if definition == nil {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	member, keys, err := proposal.UniqueOutput(bound, &evaluation, precheck, purpose)
	if err != nil {
		return storage.RowVersion{}, err
	}
	member = member && !index.UniqueHasNull(keys)
	if member && (purpose == index.ProbeRuntime && !bound.Index.SQLIndex.HasRuntimeDomainConversion() || bound.Index.SQLIndex.Method == catalog.HashMethod) {
		keys = slices.Clone(keys)
	}
	if purpose == index.ProbeRevalidation && proposal.Exclusion == nil {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	return s.writeHost().PublishExclusion(ref, binding.Catalog, proposal, index.ExclusionEdit{Index: definition.Index, Keys: keys, Member: member, Admitted: definition.Deferrable, Precheck: precheck, Excluded: excluded})
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

// exclusionScan is a captured scan with the write authority captured in the
// same window, for the conflict's disclosure.
type exclusionScan struct {
	index.ExclusionScan
	auth AuthorizationView
}

func (s *StatementScope) probeExclusion(ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, candidate storage.RowVersion, recheck bool, purpose index.ProbePurpose) (uniqueConflict, bool, error) {
	idx := bound.Index
	for {
		captured, auth, err := s.writeHost().captureExclusionScan(ref, binding, idx)
		scan := exclusionScan{captured, auth}
		if err != nil {
			return uniqueConflict{}, false, err
		}
		current, found := index.OwnExclusionEntry(&scan.ExclusionScan, s.ticket, idx.Address(), candidate)
		if !found {
			return uniqueConflict{}, false, nil
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: scan.View}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if binding.TypeName != nil {
			evaluation.Input.TypeDiagnosticName = binding.TypeName
		}
		visit, err := index.VisitExclusion(s.op, &evaluation, &scan.ExclusionScan, s.ticket, ref.Address, bound, current, purpose)
		if err != nil {
			return uniqueConflict{}, false, err
		}
		if !s.writeHost().ExclusionScanCurrent(scan.ExclusionScan, idx.Address()) {
			continue
		}
		deferred, err := index.ExclusionDeferred(binding.Parent, idx, recheck)
		if err != nil {
			return uniqueConflict{}, false, err
		}
		if deferred {
			return uniqueConflict{}, visit.Potential, nil
		}
		if visit.Wait != nil {
			if err := s.writeHost().WaitTransaction(visit.Wait, lock.CompletionTransaction); err != nil {
				return uniqueConflict{}, false, err
			}
			continue
		}
		if !visit.Hit {
			return uniqueConflict{}, visit.Potential, nil
		}
		return uniqueConflict{parent: binding.Parent, index: idx, key: visit.Key, existing: visit.Existing, auth: scan.auth, view: scan.View, schema: scan.Schema, row: visit.Row, committed: visit.Committed}, visit.Potential, nil
	}
}
func (s *StatementScope) probeExclusionRow(ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, proposal *index.WriteCandidate, excluded []storage.RowVersion, action *dml.InsertConflictState, purpose index.ProbePurpose) (uniqueConflict, error) {
	idx := bound.Index
	if action != nil && !slices.Contains(action.Selected, idx.Address()) {
		return uniqueConflict{}, nil
	}
	candidate, err := s.publishExclusionCandidate(ref, binding, bound, proposal, excluded, action != nil, purpose)
	if err != nil {
		return uniqueConflict{}, err
	}
	c := index.ExclusionDefinition(binding.Parent, idx)
	if c == nil {
		return uniqueConflict{}, ids.ErrCatalogReplaced
	}
	if action != nil && c.Deferrable {
		schema := binding.Catalog.Get(binding.Parent.Namespace()).Name()
		return uniqueConflict{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters", Schema: schema, Table: binding.Parent.Name(), Constraint: idx.Name()}
	}
	if !c.Deferrable {
		// Existing exact-key grants serialize nondeferrable equal admissions.
		keys := s.writeHost().OwnExclusionKeys(idx.Address(), candidate.Version)
		if keys == nil {
			return uniqueConflict{}, nil
		}
		indexRef := ref
		indexRef.Address = idx.Address()
		if idx.SQLIndex.ExactEqualityClaims() {
			if err := s.writeHost().ClaimUniqueKey(indexRef, bound, keys); err != nil {
				return uniqueConflict{}, err
			}
		}
		s.writeHost().AdmitExclusion(idx.Address(), candidate.Version)
	}
	conflict, potential, err := s.probeExclusion(ref, binding, bound, candidate, false, purpose)
	if err != nil {
		return uniqueConflict{}, err
	}
	if conflict.index != nil {
		return conflict, nil
	}
	if c.Deferrable && potential {
		event := index.ExclusionEvent{Parent: ref, Index: ref.Domain().Ref(ref.Epoch, idx.Address()), Constraint: c.OID, Row: candidate.Row, Version: candidate.Version}
		if !slices.Contains(s.indexesOrZero().ExclusionModes.Events, event) {
			s.indexes().ExclusionModes.Events = append(slices.Clone(s.indexesOrZero().ExclusionModes.Events), event)
			if s.foreignActionsOrZero().Settling {
				s.foreignActions().ExclusionAdded = true
			}
		}
	}
	return uniqueConflict{}, nil
}
