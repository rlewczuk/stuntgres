package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// These immutable projections are visibility, not lock grants. Engine.mu owns
// their ticket membership; all providers and completion waits run unlocked.
type exclusionEntry struct {
	row      storage.RowVersion
	keys     []sqltype.Value
	admitted bool
}
type exclusionProjection struct {
	entries map[ids.ObjectAddress][]exclusionEntry
	ended   map[ids.ObjectAddress]map[ids.RowID]ids.VersionID
}
type exclusionEvent struct {
	parent, index ids.ObjectRef
	constraint    ids.OID
	row           ids.RowID
	version       ids.VersionID
}
type exclusionModes struct {
	allSet, allDeferred bool
	byConstraint        map[ids.OID]bool
	events              []exclusionEvent
}

func (m exclusionModes) deferred(c *catalog.ExclusionConstraint) bool {
	if !c.Deferrable {
		return false
	}
	if value, ok := m.byConstraint[c.OID]; ok {
		return value
	}
	if m.allSet {
		return m.allDeferred
	}
	return c.InitiallyDeferred
}
func exclusionDefinition(parent *catalog.Relation, index *catalog.Relation) *catalog.ExclusionConstraint {
	for i := range parent.Constraints.Exclusion {
		c := &parent.Constraints.Exclusion[i]
		if c.Index == index.Address() && c.OID == index.SQLIndex.Constraint.OID {
			return c
		}
	}
	return nil
}
func bindExclusionOrdering(parent *catalog.Relation, d *catalog.IndexDefinition) (indexOrdering, error) {
	if d.Method == catalog.GistMethod {
		return bindLogicalExclusionKeys(parent, d)
	}
	if d.Method == catalog.BtreeMethod {
		return bindIndexOrdering(parent, d)
	}
	// The supported HASH equality has the same concrete scalar comparison as
	// its default Btree class. This is a comparator binding, not an index or a
	// UNIQUE owner, and HASH storage continues to use its actual provider.
	copy := *d
	copy.Method = catalog.BtreeMethod
	copy.Keys = slices.Clone(d.Keys)
	for i, key := range copy.Keys {
		u, err := indexKeyUsage(parent, key)
		if err != nil {
			return indexOrdering{}, err
		}
		class, ok := sqltype.DefaultIndexClass(u.Type)
		if !ok {
			return indexOrdering{}, fmt.Errorf("stuntgres: missing exclusion equality comparator")
		}
		copy.Keys[i].Class = class
	}
	return bindIndexOrdering(parent, &copy)
}
func exclusionKeysMatch(ctx context.Context, d *catalog.IndexDefinition, left, right []sqltype.Value) (bool, error) {
	for i, operator := range d.Exclusion {
		match, err := operator.Matches(ctx, left[i], right[i])
		if err != nil || !match {
			return false, err
		}
	}
	return true, nil
}
func cloneExclusionProjection(old *exclusionProjection) *exclusionProjection {
	p := &exclusionProjection{entries: make(map[ids.ObjectAddress][]exclusionEntry), ended: make(map[ids.ObjectAddress]map[ids.RowID]ids.VersionID)}
	if old != nil {
		p.entries = maps.Clone(old.entries)
		p.ended = maps.Clone(old.ended)
	}
	return p
}
func (s *StatementScope) beginExclusionProjectionLocked(e *Engine) {
	if s.indexesOrZero().exclusionStarted {
		return
	}
	s.indexes().exclusionStarted = true
	s.indexes().exclusionModes = s.owner.exclusionModes
}
func (s *StatementScope) installExclusionProjectionLocked(e *Engine, p *exclusionProjection) {
	if e.exclusionPending == nil {
		e.exclusionPending = make(map[*lock.Ticket]*exclusionProjection)
	}
	e.exclusionPending[s.ticket] = p
	e.exclusionGeneration++
}
func (s *StatementScope) exclusionCandidateLocked(e *Engine, proposal *indexWriteCandidate, excluded []storage.RowVersion, catalog *catalog.State) (storage.RowVersion, error) {
	if proposal.exclusion != nil {
		row := *proposal.exclusion
		row.IndexAdmission = catalog
		return row, nil
	}
	if e.storage.VersionFrontier() == ^uint64(0) || len(excluded) == 0 && e.storage.RowFrontier() == ^uint64(0) {
		return storage.RowVersion{}, fmt.Errorf("stuntgres: row identity exhausted")
	}
	row := storage.RowVersion{Version: e.storage.AllocateVersionLocked(), Values: proposal.values, IndexAdmission: catalog}
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
func (s *StatementScope) publishExclusionCandidate(ref ids.ObjectRef, binding *uniqueWriteBinding, bound *uniqueBoundKey, proposal *indexWriteCandidate, excluded []storage.RowVersion, precheck bool, purpose indexProbePurpose) (storage.RowVersion, error) {
	e := s.op.session.Engine()
	evaluation := EvalContext{evalStatement: evalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, statementScope: s, catalog: e.catalog.read(binding.catalog)}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
	if binding.typeName != nil {
		evaluation.Input.TypeDiagnosticName = binding.typeName
	}
	definition := exclusionDefinition(binding.parent, bound.index)
	if definition == nil {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	member, keys, err := proposal.uniqueOutput(bound, &evaluation, precheck, purpose)
	if err != nil {
		return storage.RowVersion{}, err
	}
	member = member && !uniqueHasNull(keys)
	if member && (purpose == indexProbeRuntime && !bound.index.SQLIndex.HasRuntimeDomainConversion() || bound.index.SQLIndex.Method == catalog.HashMethod) {
		keys = slices.Clone(keys)
	}
	if purpose == indexProbeRevalidation && proposal.exclusion == nil {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.catalog.state.Epoch() || e.privateCatalogLocked(s.owner.catalog).State() != binding.catalog {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	s.beginExclusionProjectionLocked(e)
	row, err := s.exclusionCandidateLocked(e, proposal, excluded, binding.catalog)
	if err != nil {
		return storage.RowVersion{}, err
	}
	row.IndexOutputs = proposal.indexes
	if precheck {
		row.IndexOutputs = proposal.precheck
		proposal.sharedPrecheck = true
	} else {
		proposal.sharedIndexes = true
	}
	proposal.exclusion = &row
	p := cloneExclusionProjection(e.exclusionPending[s.ticket])
	entries := p.entries[definition.Index]
	next := make([]exclusionEntry, 0, len(entries)+1)
	for _, entry := range entries {
		if entry.row.Row != row.Row {
			next = append(next, entry)
		}
	}
	if member {
		next = append(next, exclusionEntry{row: row, keys: keys, admitted: definition.Deferrable})
	}
	// A later index insertion preserves the projections of earlier indexes.
	p.entries[definition.Index] = next
	if len(excluded) != 0 {
		ends := maps.Clone(p.ended[ref.Address])
		if ends == nil {
			ends = make(map[ids.RowID]ids.VersionID)
		}
		for _, old := range excluded {
			ends[old.Row] = old.Version
			if old.Base != 0 {
				ends[old.Row] = old.Base
			}
		}
		p.ended[ref.Address] = ends
	}
	s.installExclusionProjectionLocked(e, p)
	return row, nil
}
func (s *StatementScope) discardExclusionCandidate(ref ids.ObjectRef, proposal *indexWriteCandidate) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if proposal.exclusion == nil {
		return
	}
	version := proposal.exclusion.Version
	proposal.exclusion = nil
	if version == 0 {
		return
	}
	p := cloneExclusionProjection(e.exclusionPending[s.ticket])
	for index, entries := range p.entries {
		if !slices.ContainsFunc(entries, func(e exclusionEntry) bool { return e.row.Version == version }) {
			continue
		}
		next := make([]exclusionEntry, 0, len(entries)-1)
		for _, entry := range entries {
			if entry.row.Version != version {
				next = append(next, entry)
			}
		}
		p.entries[index] = next
	}
	s.installExclusionProjectionLocked(e, p)
}
func (s *StatementScope) projectExclusionDeletionLocked(e *Engine, candidate visibility.DeleteCandidate) {
	parent, ok := e.privateCatalogLocked(s.owner.catalog).Relation(candidate.Ref.Address)
	if !ok || len(parent.Constraints.Exclusion) == 0 {
		return
	}
	s.beginExclusionProjectionLocked(e)
	p := cloneExclusionProjection(e.exclusionPending[s.ticket])
	ends := maps.Clone(p.ended[candidate.Ref.Address])
	if ends == nil {
		ends = make(map[ids.RowID]ids.VersionID)
	}
	ends[candidate.Row] = candidate.Version
	p.ended[candidate.Ref.Address] = ends
	for _, c := range parent.Constraints.Exclusion {
		entries := p.entries[c.Index]
		next := make([]exclusionEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.row.Row != candidate.Row {
				next = append(next, entry)
			}
		}
		p.entries[c.Index] = next
	}
	s.installExclusionProjectionLocked(e, p)
}

type exclusionScan struct {
	generation      uint64
	root            *storage.IndexRoot
	pending         map[*lock.Ticket]*exclusionProjection
	auth            AuthorizationView
	view            catalog.Read
	schema          string
	parent          ids.ObjectAddress
	store           *storage.Relation
	storeGeneration uint64
	versions        []storage.RowVersion
	ends            *storage.DeletionRoot
}

func (s *StatementScope) captureExclusionScan(ref ids.ObjectRef, binding *uniqueWriteBinding, index *catalog.Relation) (exclusionScan, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	view := e.privateCatalogLocked(s.owner.catalog)
	if ref.Epoch != e.catalog.state.Epoch() || view.State() != binding.catalog {
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
	root := e.storage.IndexRoots[index.Address()]
	if s.owner.catalog != nil {
		if private, ok := s.owner.catalog.indexesOrZero().uniqueRoots[index.Address()]; ok {
			root = private
		}
	}
	ns, ok := view.Namespace(binding.parent.Namespace())
	if !ok {
		return exclusionScan{}, fmt.Errorf("stuntgres: missing exclusion namespace")
	}
	store := e.privateStorageLocked(s.owner.catalog, binding.parent.Address())
	if store == nil {
		return exclusionScan{}, fmt.Errorf("stuntgres: missing exclusion row storage")
	}
	return exclusionScan{generation: e.exclusionGeneration, root: root, pending: maps.Clone(e.exclusionPending), auth: auth, view: view, schema: ns.Name(), parent: binding.parent.Address(), store: store, storeGeneration: store.Generation, versions: store.Versions, ends: store.Ends}, nil
}
func (s *StatementScope) exclusionScanCurrent(scan exclusionScan, index ids.ObjectAddress) bool {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	root := e.storage.IndexRoots[index]
	if s.owner.catalog != nil {
		if private, ok := s.owner.catalog.indexesOrZero().uniqueRoots[index]; ok {
			root = private
		}
	}
	store := e.privateStorageLocked(s.owner.catalog, scan.parent)
	return scan.generation == e.exclusionGeneration && scan.view.State() == e.privateCatalogLocked(s.owner.catalog).State() && scan.root == root && store == scan.store && store.Generation == scan.storeGeneration && len(store.Versions) == len(scan.versions) && store.Ends == scan.ends
}
func (s *StatementScope) probeExclusion(ref ids.ObjectRef, binding *uniqueWriteBinding, bound *uniqueBoundKey, candidate storage.RowVersion, recheck bool, purpose indexProbePurpose) (uniqueConflict, bool, error) {
	index := bound.index
	for {
		scan, err := s.captureExclusionScan(ref, binding, index)
		if err != nil {
			return uniqueConflict{}, false, err
		}
		own := scan.pending[s.ticket]
		var current exclusionEntry
		found := false
		if own != nil {
			for _, entry := range own.entries[index.Address()] {
				if entry.row.Row == candidate.Row && entry.row.Version == candidate.Version {
					current = entry
					found = true
					break
				}
			}
		}
		if !found {
			return uniqueConflict{}, false, nil
		}
		evaluation := EvalContext{evalStatement: evalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, statementScope: s, catalog: scan.view}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
		if binding.typeName != nil {
			evaluation.Input.TypeDiagnosticName = binding.typeName
		}
		potential := false
		var wait *lock.Ticket
		var hit uniqueConflict
		visit := func(entry exclusionEntry, owner *lock.Ticket, committed bool) error {
			if owner == s.ticket && entry.row.Row == candidate.Row {
				return nil
			}
			if owner != nil && owner != s.ticket && !entry.admitted {
				return nil
			}
			if own != nil && own.ended[ref.Address][entry.row.Row] == entry.row.Version && committed {
				return nil
			}
			match, err := exclusionKeysMatch(s.op.Context(), index.SQLIndex, current.keys, entry.keys)
			if err != nil || !match {
				return err
			}
			if purpose != indexProbeRevalidation && index.SQLIndex.HasRuntimeDomainKeyConversion() {
				if err := bound.ordering.fillKeys(entry.row.Values, bound.scratch, &evaluation); err != nil {
					return err
				}
				match, err = exclusionKeysMatch(s.op.Context(), index.SQLIndex, current.keys, bound.scratch)
				if err != nil || !match {
					return err
				}
			}
			if committed {
				for ticket, p := range scan.pending {
					if ticket != s.ticket && p.ended[ref.Address][entry.row.Row] == entry.row.Version {
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
				existing := entry.keys
				if purpose != indexProbeRevalidation && index.SQLIndex.HasRuntimeDomainKeyConversion() {
					existing = slices.Clone(bound.scratch)
				} else if committed && scan.root.Hash != nil {
					existing = slices.Clone(existing)
				}
				hit = uniqueConflict{parent: binding.parent, index: index, key: current.keys, existing: existing, auth: scan.auth, view: scan.view, schema: scan.schema, row: entry.row, committed: committed}
			}
			return nil
		}
		if scan.root != nil {
			if scan.root.Hash != nil {
				var keys [1]sqltype.Value
				for _, bucket := range scan.root.Hash.Buckets {
					for _, entry := range bucket {
						row, err := conflictEntryVersion(scan.versions, storage.IndexEntry{Row: entry.Row, Version: entry.Version, Position: entry.Position})
						if err != nil {
							return uniqueConflict{}, false, err
						}
						keys[0] = entry.Key
						if err := visit(exclusionEntry{row: row, keys: keys[:]}, nil, true); err != nil {
							return uniqueConflict{}, false, err
						}
					}
				}
			} else {
				for _, entry := range scan.root.Entries {
					row, err := conflictEntryVersion(scan.versions, entry)
					if err != nil {
						return uniqueConflict{}, false, err
					}
					if err := visit(exclusionEntry{row: row, keys: entry.Keys}, nil, true); err != nil {
						return uniqueConflict{}, false, err
					}
				}
			}
		}
		for ticket, p := range scan.pending {
			for _, entry := range p.entries[index.Address()] {
				if err := visit(entry, ticket, false); err != nil {
					return uniqueConflict{}, false, err
				}
			}
		}
		if !s.exclusionScanCurrent(scan, index.Address()) {
			continue
		}
		c := exclusionDefinition(binding.parent, index)
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
func (s *StatementScope) probeExclusionRow(ref ids.ObjectRef, binding *uniqueWriteBinding, bound *uniqueBoundKey, proposal *indexWriteCandidate, excluded []storage.RowVersion, action *boundInsertConflict, purpose indexProbePurpose) (uniqueConflict, error) {
	index := bound.index
	if action != nil && !slices.Contains(action.selected, index.Address()) {
		return uniqueConflict{}, nil
	}
	candidate, err := s.publishExclusionCandidate(ref, binding, bound, proposal, excluded, action != nil, purpose)
	if err != nil {
		return uniqueConflict{}, err
	}
	c := exclusionDefinition(binding.parent, index)
	if c == nil {
		return uniqueConflict{}, ids.ErrCatalogReplaced
	}
	if action != nil && c.Deferrable {
		schema := binding.catalog.Get(binding.parent.Namespace()).Name()
		return uniqueConflict{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters", Schema: schema, Table: binding.parent.Name(), Constraint: index.Name()}
	}
	if !c.Deferrable {
		// Existing exact-key grants serialize nondeferrable equal admissions.
		p := s.op.session.Engine()
		p.mu.Lock()
		projection := p.exclusionPending[s.ticket]
		var keys []sqltype.Value
		if projection != nil {
			for _, entry := range projection.entries[index.Address()] {
				if entry.row.Version == candidate.Version {
					keys = entry.keys
					break
				}
			}
		}
		p.mu.Unlock()
		if keys == nil {
			return uniqueConflict{}, nil
		}
		indexRef := ref
		indexRef.Address = index.Address()
		if index.SQLIndex.ExactEqualityClaims() {
			if err := s.acquireUnique(indexRef, bound, keys); err != nil {
				return uniqueConflict{}, err
			}
		}
		p.mu.Lock()
		projected := cloneExclusionProjection(p.exclusionPending[s.ticket])
		entries := slices.Clone(projected.entries[index.Address()])
		for at := range entries {
			if entries[at].row.Version == candidate.Version {
				entries[at].admitted = true
			}
		}
		projected.entries[index.Address()] = entries
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
		event := exclusionEvent{parent: ref, index: ref.Domain().Ref(ref.Epoch, index.Address()), constraint: c.OID, row: candidate.Row, version: candidate.Version}
		if !slices.Contains(s.indexesOrZero().exclusionModes.events, event) {
			s.indexes().exclusionModes.events = append(slices.Clone(s.indexesOrZero().exclusionModes.events), event)
			if s.foreignActionsOrZero().settling {
				s.foreignActions().exclusionAdded = true
			}
		}
	}
	return uniqueConflict{}, nil
}
