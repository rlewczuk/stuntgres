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
func publishExclusionCandidate(h dml.Host, ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, proposal *index.WriteCandidate, excluded []storage.RowVersion, precheck bool, purpose index.ProbePurpose) (storage.RowVersion, error) {
	evaluation := bindingEvaluation(h, binding)
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
	return h.PublishExclusion(ref, binding.Catalog, proposal, index.ExclusionEdit{Index: definition.Index, Keys: keys, Member: member, Admitted: definition.Deferrable, Precheck: precheck, Excluded: excluded})
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

func probeExclusion(h dml.Host, ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, candidate storage.RowVersion, recheck bool, purpose index.ProbePurpose) (uniqueConflict, bool, error) {
	idx := bound.Index
	for {
		scan, err := h.CaptureExclusionScan(ref, binding, idx)
		if err != nil {
			return uniqueConflict{}, false, err
		}
		ticket := h.Ticket()
		current, found := index.OwnExclusionEntry(&scan, ticket, idx.Address(), candidate)
		if !found {
			return uniqueConflict{}, false, nil
		}
		input, output := h.Environments()
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: h.Context(), Input: input, Output: output, Host: h, Catalog: scan.View}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if binding.TypeName != nil {
			evaluation.Input.TypeDiagnosticName = binding.TypeName
		}
		visit, err := index.VisitExclusion(h, &evaluation, &scan, ticket, ref.Address, bound, current, purpose)
		if err != nil {
			return uniqueConflict{}, false, err
		}
		if !h.ExclusionScanCurrent(scan, idx.Address()) {
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
			if err := h.WaitTransaction(visit.Wait, lock.CompletionTransaction); err != nil {
				return uniqueConflict{}, false, err
			}
			continue
		}
		if !visit.Hit {
			return uniqueConflict{}, visit.Potential, nil
		}
		return uniqueConflict{parent: binding.Parent, index: idx, key: visit.Key, existing: visit.Existing, auth: scan.Authority, view: scan.View, schema: scan.Schema, row: visit.Row, committed: visit.Committed}, visit.Potential, nil
	}
}
func probeExclusionRow(h dml.Host, ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, proposal *index.WriteCandidate, excluded []storage.RowVersion, action *dml.InsertConflictState, purpose index.ProbePurpose) (uniqueConflict, error) {
	idx := bound.Index
	if action != nil && !slices.Contains(action.Selected, idx.Address()) {
		return uniqueConflict{}, nil
	}
	candidate, err := publishExclusionCandidate(h, ref, binding, bound, proposal, excluded, action != nil, purpose)
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
		keys := h.OwnExclusionKeys(idx.Address(), candidate.Version)
		if keys == nil {
			return uniqueConflict{}, nil
		}
		indexRef := ref
		indexRef.Address = idx.Address()
		if idx.SQLIndex.ExactEqualityClaims() {
			if err := h.ClaimUniqueKey(indexRef, bound, keys); err != nil {
				return uniqueConflict{}, err
			}
		}
		h.AdmitExclusion(idx.Address(), candidate.Version)
	}
	conflict, potential, err := probeExclusion(h, ref, binding, bound, candidate, false, purpose)
	if err != nil {
		return uniqueConflict{}, err
	}
	if conflict.index != nil {
		return conflict, nil
	}
	if c.Deferrable && potential {
		event := index.ExclusionEvent{Parent: ref, Index: ref.Domain().Ref(ref.Epoch, idx.Address()), Constraint: c.OID, Row: candidate.Row, Version: candidate.Version}
		var events []index.ExclusionEvent
		if scope := h.IndexScope(false); scope != nil {
			events = scope.ExclusionModes.Events
		}
		if !slices.Contains(events, event) {
			h.IndexScope(true).ExclusionModes.Events = append(slices.Clone(events), event)
			if actions := h.ForeignActions(false); actions != nil && actions.Settling {
				actions.ExclusionAdded = true
			}
		}
	}
	return uniqueConflict{}, nil
}
