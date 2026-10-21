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
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.catalog.state.Epoch() || e.privateCatalogLocked(s.owner.Catalog).State() != binding.Catalog {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	s.beginExclusionProjectionLocked(e)
	row, err := s.exclusionCandidateLocked(e, proposal, excluded, binding.Catalog)
	if err != nil {
		return storage.RowVersion{}, err
	}
	row.IndexOutputs = proposal.Indexes
	if precheck {
		row.IndexOutputs = proposal.Precheck
		proposal.SharedPrecheck = true
	} else {
		proposal.SharedIndexes = true
	}
	proposal.Exclusion = &row
	p := e.exclusions.ProjectionLocked(s.ticket)
	entries := p.Entries[definition.Index]
	next := make([]index.ExclusionEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.Row.Row != row.Row {
			next = append(next, entry)
		}
	}
	if member {
		next = append(next, index.ExclusionEntry{Row: row, Keys: keys, Admitted: definition.Deferrable})
	}
	// A later index insertion preserves the projections of earlier indexes.
	p.Entries[definition.Index] = next
	if len(excluded) != 0 {
		ends := maps.Clone(p.Ended[ref.Address])
		if ends == nil {
			ends = make(map[ids.RowID]ids.VersionID)
		}
		for _, old := range excluded {
			ends[old.Row] = old.Version
			if old.Base != 0 {
				ends[old.Row] = old.Base
			}
		}
		p.Ended[ref.Address] = ends
	}
	s.installExclusionProjectionLocked(e, p)
	return row, nil
}
func (s *StatementScope) discardExclusionCandidate(ref ids.ObjectRef, proposal *index.WriteCandidate) {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if proposal.Exclusion == nil {
		return
	}
	version := proposal.Exclusion.Version
	proposal.Exclusion = nil
	if version == 0 {
		return
	}
	p := e.exclusions.ProjectionLocked(s.ticket)
	for idx, entries := range p.Entries {
		if !slices.ContainsFunc(entries, func(e index.ExclusionEntry) bool { return e.Row.Version == version }) {
			continue
		}
		next := make([]index.ExclusionEntry, 0, len(entries)-1)
		for _, entry := range entries {
			if entry.Row.Version != version {
				next = append(next, entry)
			}
		}
		p.Entries[idx] = next
	}
	s.installExclusionProjectionLocked(e, p)
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

type exclusionScan struct {
	generation      uint64
	root            *storage.IndexRoot
	pending         map[*lock.Ticket]*index.ExclusionProjection
	auth            AuthorizationView
	view            catalog.Read
	schema          string
	parent          ids.ObjectAddress
	store           *storage.Relation
	storeGeneration uint64
	versions        []storage.RowVersion
	ends            *storage.DeletionRoot
}

func (s *StatementScope) captureExclusionScan(ref ids.ObjectRef, binding *index.UniqueWriteBinding, idx *catalog.Relation) (exclusionScan, error) {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	view := e.privateCatalogLocked(s.owner.Catalog)
	if ref.Epoch != e.catalog.state.Epoch() || view.State() != binding.Catalog {
		return exclusionScan{}, ids.ErrCatalogReplaced
	}
	auth, err := s.writeAuthorizationLocked(e)
	if err != nil {
		return exclusionScan{}, err
	}
	if admission := s.foreignActionsOrZero().admissions[ref.Address]; admission != nil && admission.mutated {
		auth = foreignActionOwner(auth, admission.relation.Owner)
	}
	auth.Catalog = view
	auth.Recapture(auth.Catalog)
	root := e.storage.IndexRoots[idx.Address()]
	if s.owner.Catalog != nil {
		if private, ok := index.JournalIndexesOrZero(s.owner.Catalog).UniqueRoots[idx.Address()]; ok {
			root = private
		}
	}
	ns, ok := view.Namespace(binding.Parent.Namespace())
	if !ok {
		return exclusionScan{}, fmt.Errorf("stuntgres: missing exclusion namespace")
	}
	store := e.privateStorageLocked(s.owner.Catalog, binding.Parent.Address())
	if store == nil {
		return exclusionScan{}, fmt.Errorf("stuntgres: missing exclusion row storage")
	}
	generation, pending := e.exclusions.ScanLocked()
	return exclusionScan{generation: generation, root: root, pending: pending, auth: auth, view: view, schema: ns.Name(), parent: binding.Parent.Address(), store: store, storeGeneration: store.Generation, versions: store.Versions, ends: store.Ends}, nil
}
func (s *StatementScope) exclusionScanCurrent(scan exclusionScan, idx ids.ObjectAddress) bool {
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	root := e.storage.IndexRoots[idx]
	if s.owner.Catalog != nil {
		if private, ok := index.JournalIndexesOrZero(s.owner.Catalog).UniqueRoots[idx]; ok {
			root = private
		}
	}
	store := e.privateStorageLocked(s.owner.Catalog, scan.parent)
	return e.exclusions.CurrentLocked(scan.generation) && scan.view.State() == e.privateCatalogLocked(s.owner.Catalog).State() && scan.root == root && store == scan.store && store.Generation == scan.storeGeneration && len(store.Versions) == len(scan.versions) && store.Ends == scan.ends
}
func (s *StatementScope) probeExclusion(ref ids.ObjectRef, binding *index.UniqueWriteBinding, bound *index.UniqueBoundKey, candidate storage.RowVersion, recheck bool, purpose index.ProbePurpose) (uniqueConflict, bool, error) {
	idx := bound.Index
	for {
		scan, err := s.captureExclusionScan(ref, binding, idx)
		if err != nil {
			return uniqueConflict{}, false, err
		}
		own := scan.pending[s.ticket]
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
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: scan.view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
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
				for ticket, p := range scan.pending {
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
				} else if committed && scan.root.Hash != nil {
					existing = slices.Clone(existing)
				}
				hit = uniqueConflict{parent: binding.Parent, index: idx, key: current.Keys, existing: existing, auth: scan.auth, view: scan.view, schema: scan.schema, row: entry.Row, committed: committed}
			}
			return nil
		}
		if scan.root != nil {
			if scan.root.Hash != nil {
				var keys [1]sqltype.Value
				for _, bucket := range scan.root.Hash.Buckets {
					for _, entry := range bucket {
						row, err := index.ConflictEntryVersion(scan.versions, storage.IndexEntry{Row: entry.Row, Version: entry.Version, Position: entry.Position})
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
				for _, entry := range scan.root.Entries {
					row, err := index.ConflictEntryVersion(scan.versions, entry)
					if err != nil {
						return uniqueConflict{}, false, err
					}
					if err := visit(index.ExclusionEntry{Row: row, Keys: entry.Keys}, nil, true); err != nil {
						return uniqueConflict{}, false, err
					}
				}
			}
		}
		for ticket, p := range scan.pending {
			for _, entry := range p.Entries[idx.Address()] {
				if err := visit(entry, ticket, false); err != nil {
					return uniqueConflict{}, false, err
				}
			}
		}
		if !s.exclusionScanCurrent(scan, idx.Address()) {
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
			if err := s.waitCompletion(wait, lock.CompletionTransaction); err != nil {
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
		p := s.engine()
		p.mu.Lock()
		projection := p.exclusions.OwnLocked(s.ticket)
		var keys []sqltype.Value
		if projection != nil {
			for _, entry := range projection.Entries[idx.Address()] {
				if entry.Row.Version == candidate.Version {
					keys = entry.Keys
					break
				}
			}
		}
		p.mu.Unlock()
		if keys == nil {
			return uniqueConflict{}, nil
		}
		indexRef := ref
		indexRef.Address = idx.Address()
		if idx.SQLIndex.ExactEqualityClaims() {
			if err := s.acquireUnique(indexRef, bound, keys); err != nil {
				return uniqueConflict{}, err
			}
		}
		p.mu.Lock()
		projected := p.exclusions.ProjectionLocked(s.ticket)
		entries := slices.Clone(projected.Entries[idx.Address()])
		for at := range entries {
			if entries[at].Row.Version == candidate.Version {
				entries[at].Admitted = true
			}
		}
		projected.Entries[idx.Address()] = entries
		s.installExclusionProjectionLocked(p, projected)
		p.mu.Unlock()
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
