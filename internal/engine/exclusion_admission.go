package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
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
	ends := maps.Clone(p.Ended[candidate.Ref.Address])
	if ends == nil {
		ends = make(map[ids.RowID]ids.VersionID)
	}
	ends[candidate.Row] = candidate.Version
	p.Ended[candidate.Ref.Address] = ends
	for _, c := range parent.Constraints.Exclusion {
		entries := p.Entries[c.Index]
		next := make([]index.ExclusionEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.Row.Row != candidate.Row {
				next = append(next, entry)
			}
		}
		p.Entries[c.Index] = next
	}
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
		own := scan.Pending[s.ticket]
		var current index.ExclusionEntry
		found := false
		if own != nil {
			for _, entry := range own.Entries[idx.Address()] {
				if entry.Row.Row == candidate.Row && entry.Row.Version == candidate.Version {
					current = entry
					found = true
					break
				}
			}
		}
		if !found {
			return uniqueConflict{}, false, nil
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: scan.View}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
		if binding.TypeName != nil {
			evaluation.Input.TypeDiagnosticName = binding.TypeName
		}
		potential := false
		var wait *lock.Ticket
		var hit uniqueConflict
		visit := func(entry index.ExclusionEntry, owner *lock.Ticket, committed bool) error {
			if owner == s.ticket && entry.Row.Row == candidate.Row {
				return nil
			}
			if owner != nil && owner != s.ticket && !entry.Admitted {
				return nil
			}
			if own != nil && own.Ended[ref.Address][entry.Row.Row] == entry.Row.Version && committed {
				return nil
			}
			match, err := index.ExclusionKeysMatch(s.op.Context(), idx.SQLIndex, current.Keys, entry.Keys)
			if err != nil || !match {
				return err
			}
			if purpose != index.ProbeRevalidation && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
				if err := bound.Ordering.FillKeys(entry.Row.Values, bound.Scratch, &evaluation); err != nil {
					return err
				}
				match, err = index.ExclusionKeysMatch(s.op.Context(), idx.SQLIndex, current.Keys, bound.Scratch)
				if err != nil || !match {
					return err
				}
			}
			if committed {
				for ticket, p := range scan.Pending {
					if ticket != s.ticket && p.Ended[ref.Address][entry.Row.Row] == entry.Row.Version {
						potential = true
						wait = ticket
						return nil
					}
				}
			}
			potential = true
			if owner != nil && owner != s.ticket {
				wait = owner
				return nil
			}
			if hit.index == nil {
				existing := entry.Keys
				if purpose != index.ProbeRevalidation && idx.SQLIndex.HasRuntimeDomainKeyConversion() {
					existing = slices.Clone(bound.Scratch)
				} else if committed && scan.Root.Hash != nil {
					existing = slices.Clone(existing)
				}
				hit = uniqueConflict{parent: binding.Parent, index: idx, key: current.Keys, existing: existing, auth: scan.auth, view: scan.View, schema: scan.Schema, row: entry.Row, committed: committed}
			}
			return nil
		}
		if scan.Root != nil {
			if scan.Root.Hash != nil {
				var keys [1]sqltype.Value
				for _, bucket := range scan.Root.Hash.Buckets {
					for _, entry := range bucket {
						row, err := index.ConflictEntryVersion(scan.Versions, storage.IndexEntry{Row: entry.Row, Version: entry.Version, Position: entry.Position})
						if err != nil {
							return uniqueConflict{}, false, err
						}
						keys[0] = entry.Key
						if err := visit(index.ExclusionEntry{Row: row, Keys: keys[:]}, nil, true); err != nil {
							return uniqueConflict{}, false, err
						}
					}
				}
			} else {
				for _, entry := range scan.Root.Entries {
					row, err := index.ConflictEntryVersion(scan.Versions, entry)
					if err != nil {
						return uniqueConflict{}, false, err
					}
					if err := visit(index.ExclusionEntry{Row: row, Keys: entry.Keys}, nil, true); err != nil {
						return uniqueConflict{}, false, err
					}
				}
			}
		}
		for ticket, p := range scan.Pending {
			for _, entry := range p.Entries[idx.Address()] {
				if err := visit(entry, ticket, false); err != nil {
					return uniqueConflict{}, false, err
				}
			}
		}
		if !s.writeHost().ExclusionScanCurrent(scan.ExclusionScan, idx.Address()) {
			continue
		}
		c := index.ExclusionDefinition(binding.Parent, idx)
		if c == nil {
			return uniqueConflict{}, false, ids.ErrCatalogReplaced
		}
		if c.Deferrable && !recheck {
			return uniqueConflict{}, potential, nil
		}
		if wait != nil {
			if err := s.writeHost().WaitTransaction(wait, lock.CompletionTransaction); err != nil {
				return uniqueConflict{}, false, err
			}
			continue
		}
		return hit, potential, nil
	}
}
func (s *StatementScope) probeExclusionRow(ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, proposal *index.WriteCandidate, excluded []storage.RowVersion, action *insertConflictState, purpose index.ProbePurpose) (uniqueConflict, error) {
	idx := bound.Index
	if action != nil && !slices.Contains(action.selected, idx.Address()) {
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
			if s.foreignActionsOrZero().settling {
				s.foreignActions().exclusionAdded = true
			}
		}
	}
	return uniqueConflict{}, nil
}
