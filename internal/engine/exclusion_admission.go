package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// These immutable projections are visibility, not lock grants. Engine.mu owns
// their ticket membership; all providers and completion waits run unlocked.
type exclusionEntry struct {
	row      rowVersion
	keys     []sqltype.Value
	admitted bool
}
type exclusionProjection struct {
	entries map[ObjectAddress][]exclusionEntry
	ended   map[ObjectAddress]map[RowID]VersionID
}
type exclusionEvent struct {
	parent, index ObjectRef
	constraint    OID
	row           RowID
	version       VersionID
}
type exclusionModes struct {
	allSet, allDeferred bool
	byConstraint        map[OID]bool
	events              []exclusionEvent
}

func (m exclusionModes) deferred(c *exclusionConstraint) bool {
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
func exclusionDefinition(parent *relationDescriptor, index *relationDescriptor) *exclusionConstraint {
	for i := range parent.exclusionConstraints {
		c := &parent.exclusionConstraints[i]
		if c.Index == index.address && c.OID == index.sqlIndex.constraint.OID {
			return c
		}
	}
	return nil
}
func bindExclusionOrdering(parent *relationDescriptor, d *indexDefinition) (indexOrdering, error) {
	if d.method == btreeMethod {
		return bindIndexOrdering(parent, d)
	}
	// The supported HASH equality has the same concrete scalar comparison as
	// its default Btree class. This is a comparator binding, not an index or a
	// UNIQUE owner, and HASH storage continues to use its actual provider.
	copy := *d
	copy.method = btreeMethod
	copy.keys = slices.Clone(d.keys)
	for i, key := range copy.keys {
		u, err := indexKeyUsage(parent, key)
		if err != nil {
			return indexOrdering{}, err
		}
		class, ok := sqltype.DefaultIndexClass(u.Type)
		if !ok {
			return indexOrdering{}, fmt.Errorf("stuntgres: missing exclusion equality comparator")
		}
		copy.keys[i].class = class
	}
	return bindIndexOrdering(parent, &copy)
}
func exclusionKeysEqual(ctx context.Context, d *indexDefinition, left, right []sqltype.Value) (bool, error) {
	for i, operator := range d.exclusion {
		match, err := operator.Equal(ctx, left[i], right[i])
		if err != nil || !match {
			return false, err
		}
	}
	return true, nil
}
func cloneExclusionProjection(old *exclusionProjection) *exclusionProjection {
	p := &exclusionProjection{entries: make(map[ObjectAddress][]exclusionEntry), ended: make(map[ObjectAddress]map[RowID]VersionID)}
	if old != nil {
		p.entries = maps.Clone(old.entries)
		p.ended = maps.Clone(old.ended)
	}
	return p
}
func (s *StatementScope) beginExclusionProjectionLocked(e *Engine) {
	if s.exclusionStarted {
		return
	}
	s.exclusionStarted = true
	s.exclusionModes = s.owner.exclusionModes
}
func (s *StatementScope) installExclusionProjectionLocked(e *Engine, p *exclusionProjection) {
	if e.exclusionPending == nil {
		e.exclusionPending = make(map[*relationTicket]*exclusionProjection)
	}
	e.exclusionPending[s.ticket] = p
	e.exclusionGeneration++
}
func (s *StatementScope) exclusionCandidateLocked(e *Engine, ref ObjectRef, values []sqltype.Value, excluded []rowVersion, catalog *catalogState) (rowVersion, error) {
	for _, row := range s.exclusionCandidates[ref.Address] {
		if len(values) > 0 && &row.values[0] == &values[0] {
			return row, nil
		}
	}
	if e.nextVersion == ^uint64(0) || len(excluded) == 0 && e.nextRow == ^uint64(0) {
		return rowVersion{}, fmt.Errorf("stuntgres: row identity exhausted")
	}
	e.nextVersion++
	row := rowVersion{version: VersionID(e.nextVersion), values: values, indexAdmission: catalog}
	if len(excluded) == 0 {
		e.nextRow++
		row.row = RowID(e.nextRow)
	} else {
		predecessor := excluded[len(excluded)-1]
		row.row, row.base = predecessor.row, predecessor.base
		if row.base == 0 && predecessor.born != 0 {
			row.base = predecessor.version
		}
		row.keyChangedAt, row.pendingKeyChange = predecessor.keyChangedAt, predecessor.pendingKeyChange
	}
	if s.exclusionCandidates == nil {
		s.exclusionCandidates = make(map[ObjectAddress][]rowVersion)
	}
	s.exclusionCandidates[ref.Address] = append(s.exclusionCandidates[ref.Address], row)
	return row, nil
}
func (s *StatementScope) publishExclusionCandidate(ref ObjectRef, binding *uniqueWriteBinding, values []sqltype.Value, excluded []rowVersion) (rowVersion, error) {
	if len(binding.parent.exclusionConstraints) == 0 {
		return rowVersion{}, nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	if cached, ok := s.reservedExclusionRow(ref, values); ok && cached.indexAdmission == binding.catalog {
		e.mu.Unlock()
		return cached, nil
	}
	e.mu.Unlock()
	var projected []struct {
		index ObjectAddress
		entry exclusionEntry
	}
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, Input: s.input, Output: s.output, statementScope: s}
	for i := range binding.keys {
		bound := &binding.keys[i]
		d := bound.index.sqlIndex
		if !d.isExclusion() {
			continue
		}
		matches, err := indexPredicateMatches(&evaluation, d.predicate, values)
		if err != nil {
			return rowVersion{}, err
		}
		if !matches {
			continue
		}
		keys := make([]sqltype.Value, len(d.keys))
		if err := bound.ordering.fillKeys(values, keys, &evaluation); err != nil {
			return rowVersion{}, err
		}
		if uniqueHasNull(keys) {
			continue
		}
		projected = append(projected, struct {
			index ObjectAddress
			entry exclusionEntry
		}{bound.index.address, exclusionEntry{keys: keys, admitted: exclusionDefinition(binding.parent, bound.index).Deferrable}})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Epoch != e.state.epoch || e.privateCatalogLocked(s.owner.catalog).state != binding.catalog {
		return rowVersion{}, ErrCatalogReplaced
	}
	s.beginExclusionProjectionLocked(e)
	row, err := s.exclusionCandidateLocked(e, ref, values, excluded, binding.catalog)
	if err != nil {
		return rowVersion{}, err
	}
	p := cloneExclusionProjection(e.exclusionPending[s.ticket])
	for _, definition := range binding.parent.exclusionConstraints {
		entries := p.entries[definition.Index]
		next := make([]exclusionEntry, 0, len(entries)+1)
		for _, entry := range entries {
			if entry.row.row != row.row {
				next = append(next, entry)
			}
		}
		for _, candidate := range projected {
			if candidate.index == definition.Index {
				candidate.entry.row = row
				next = append(next, candidate.entry)
				break
			}
		}
		p.entries[definition.Index] = next
	}
	if len(excluded) != 0 {
		ends := maps.Clone(p.ended[ref.Address])
		if ends == nil {
			ends = make(map[RowID]VersionID)
		}
		for _, old := range excluded {
			ends[old.row] = old.version
			if old.base != 0 {
				ends[old.row] = old.base
			}
		}
		p.ended[ref.Address] = ends
	}
	s.installExclusionProjectionLocked(e, p)
	return row, nil
}
func (s *StatementScope) discardExclusionCandidate(ref ObjectRef, values []sqltype.Value) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	var version VersionID
	for _, row := range s.exclusionCandidates[ref.Address] {
		if len(values) > 0 && &row.values[0] == &values[0] {
			version = row.version
			break
		}
	}
	if version == 0 {
		return
	}
	p := cloneExclusionProjection(e.exclusionPending[s.ticket])
	for index, entries := range p.entries {
		if !slices.ContainsFunc(entries, func(e exclusionEntry) bool { return e.row.version == version }) {
			continue
		}
		next := make([]exclusionEntry, 0, len(entries)-1)
		for _, entry := range entries {
			if entry.row.version != version {
				next = append(next, entry)
			}
		}
		p.entries[index] = next
	}
	s.installExclusionProjectionLocked(e, p)
}
func (s *StatementScope) projectExclusionDeletionLocked(e *Engine, candidate deleteCandidate) {
	parent, ok := e.privateCatalogLocked(s.owner.catalog).state.descriptors[candidate.ref.Address].(*relationDescriptor)
	if !ok || len(parent.exclusionConstraints) == 0 {
		return
	}
	s.beginExclusionProjectionLocked(e)
	p := cloneExclusionProjection(e.exclusionPending[s.ticket])
	ends := maps.Clone(p.ended[candidate.ref.Address])
	if ends == nil {
		ends = make(map[RowID]VersionID)
	}
	ends[candidate.row] = candidate.version
	p.ended[candidate.ref.Address] = ends
	for _, c := range parent.exclusionConstraints {
		entries := p.entries[c.Index]
		next := make([]exclusionEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.row.row != candidate.row {
				next = append(next, entry)
			}
		}
		p.entries[c.Index] = next
	}
	s.installExclusionProjectionLocked(e, p)
}

type exclusionScan struct {
	generation      uint64
	root            *logicalIndexRoot
	pending         map[*relationTicket]*exclusionProjection
	auth            AuthorizationView
	view            CatalogRead
	schema          string
	parent          ObjectAddress
	store           *relationStorage
	storeGeneration uint64
	versions        []rowVersion
	ends            *deletionRoot
}

func (s *StatementScope) captureExclusionScan(ref ObjectRef, binding *uniqueWriteBinding, index *relationDescriptor) (exclusionScan, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	view := e.privateCatalogLocked(s.owner.catalog)
	if ref.Epoch != e.state.epoch || view.state != binding.catalog {
		return exclusionScan{}, ErrCatalogReplaced
	}
	auth, err := s.writeAuthorizationLocked(e)
	if err != nil {
		return exclusionScan{}, err
	}
	if admission := s.foreignActionAdmissions[ref.Address]; admission != nil && admission.mutated {
		auth = foreignActionOwner(auth, admission.relation.owner)
	}
	auth.catalog = view
	auth.roles.catalog = auth.catalog
	root := e.indexRoots[index.address]
	if s.owner.catalog != nil {
		if private, ok := s.owner.catalog.uniqueRoots[index.address]; ok {
			root = private
		}
	}
	ns, ok := view.state.descriptors[binding.parent.namespace].(*namespaceDescriptor)
	if !ok {
		return exclusionScan{}, fmt.Errorf("stuntgres: missing exclusion namespace")
	}
	store := e.privateStorageLocked(s.owner.catalog, binding.parent.address)
	if store == nil {
		return exclusionScan{}, fmt.Errorf("stuntgres: missing exclusion row storage")
	}
	return exclusionScan{generation: e.exclusionGeneration, root: root, pending: maps.Clone(e.exclusionPending), auth: auth, view: view, schema: ns.name, parent: binding.parent.address, store: store, storeGeneration: store.generation, versions: store.versions, ends: store.ends}, nil
}
func (s *StatementScope) exclusionScanCurrent(scan exclusionScan, index ObjectAddress) bool {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	root := e.indexRoots[index]
	if s.owner.catalog != nil {
		if private, ok := s.owner.catalog.uniqueRoots[index]; ok {
			root = private
		}
	}
	store := e.privateStorageLocked(s.owner.catalog, scan.parent)
	return scan.generation == e.exclusionGeneration && scan.view.state == e.privateCatalogLocked(s.owner.catalog).state && scan.root == root && store == scan.store && store.generation == scan.storeGeneration && len(store.versions) == len(scan.versions) && store.ends == scan.ends
}
func (s *StatementScope) probeExclusion(ref ObjectRef, binding *uniqueWriteBinding, bound *uniqueBoundKey, candidate rowVersion, recheck bool) (uniqueConflict, bool, error) {
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
			for _, entry := range own.entries[index.address] {
				if entry.row.row == candidate.row && entry.row.version == candidate.version {
					current = entry
					found = true
					break
				}
			}
		}
		if !found {
			return uniqueConflict{}, false, nil
		}
		potential := false
		var wait *relationTicket
		var hit uniqueConflict
		visit := func(entry exclusionEntry, owner *relationTicket, committed bool) error {
			if owner == s.ticket && entry.row.row == candidate.row {
				return nil
			}
			if owner != nil && owner != s.ticket && !entry.admitted {
				return nil
			}
			if own != nil && own.ended[ref.Address][entry.row.row] == entry.row.version && committed {
				return nil
			}
			match, err := exclusionKeysEqual(s.op.Context(), index.sqlIndex, current.keys, entry.keys)
			if err != nil || !match {
				return err
			}
			if committed {
				for ticket, p := range scan.pending {
					if ticket != s.ticket && p.ended[ref.Address][entry.row.row] == entry.row.version {
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
				if committed && scan.root.hash != nil {
					existing = slices.Clone(existing)
				}
				hit = uniqueConflict{parent: binding.parent, index: index, key: current.keys, existing: existing, auth: scan.auth, view: scan.view, schema: scan.schema, row: entry.row, committed: committed}
			}
			return nil
		}
		if scan.root != nil {
			if scan.root.hash != nil {
				var keys [1]sqltype.Value
				for _, bucket := range scan.root.hash.buckets {
					for _, entry := range bucket {
						row, err := conflictEntryVersion(scan.versions, logicalIndexEntry{row: entry.row, version: entry.version, position: entry.position})
						if err != nil {
							return uniqueConflict{}, false, err
						}
						keys[0] = entry.key
						if err := visit(exclusionEntry{row: row, keys: keys[:]}, nil, true); err != nil {
							return uniqueConflict{}, false, err
						}
					}
				}
			} else {
				for _, entry := range scan.root.entries {
					row, err := conflictEntryVersion(scan.versions, entry)
					if err != nil {
						return uniqueConflict{}, false, err
					}
					if err := visit(exclusionEntry{row: row, keys: entry.keys}, nil, true); err != nil {
						return uniqueConflict{}, false, err
					}
				}
			}
		}
		for ticket, p := range scan.pending {
			for _, entry := range p.entries[index.address] {
				if err := visit(entry, ticket, false); err != nil {
					return uniqueConflict{}, false, err
				}
			}
		}
		if !s.exclusionScanCurrent(scan, index.address) {
			continue
		}
		c := exclusionDefinition(binding.parent, index)
		if c == nil {
			return uniqueConflict{}, false, ErrCatalogReplaced
		}
		if c.Deferrable && !recheck {
			return uniqueConflict{}, potential, nil
		}
		if wait != nil {
			if err := s.waitCompletion(wait); err != nil {
				return uniqueConflict{}, false, err
			}
			continue
		}
		return hit, potential, nil
	}
}
func (s *StatementScope) probeExclusionRow(ref ObjectRef, values []sqltype.Value, excluded []rowVersion, action *boundInsertConflict) (uniqueConflict, error) {
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return uniqueConflict{}, err
	}
	candidate, err := s.publishExclusionCandidate(ref, binding, values, excluded)
	if err != nil {
		return uniqueConflict{}, err
	}
	for i := range binding.keys {
		bound := &binding.keys[i]
		index := bound.index
		if !index.sqlIndex.isExclusion() {
			continue
		}
		if action != nil && !slices.Contains(action.selected, index.address) {
			continue
		}
		c := exclusionDefinition(binding.parent, index)
		if c == nil {
			return uniqueConflict{}, ErrCatalogReplaced
		}
		if action != nil && c.Deferrable {
			schema := binding.catalog.descriptors[binding.parent.namespace].Name()
			return uniqueConflict{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters", Schema: schema, Table: binding.parent.name, Constraint: index.name}
		}
		if !c.Deferrable {
			// Existing exact-key grants serialize nondeferrable equal admissions.
			p := s.op.session.Engine()
			p.mu.Lock()
			projection := p.exclusionPending[s.ticket]
			var keys []sqltype.Value
			if projection != nil {
				for _, entry := range projection.entries[index.address] {
					if entry.row.version == candidate.version {
						keys = entry.keys
						break
					}
				}
			}
			p.mu.Unlock()
			if keys == nil {
				continue
			}
			indexRef := ref
			indexRef.Address = index.address
			if err := s.acquireUnique(indexRef, bound, keys); err != nil {
				return uniqueConflict{}, err
			}
			p.mu.Lock()
			projected := cloneExclusionProjection(p.exclusionPending[s.ticket])
			entries := slices.Clone(projected.entries[index.address])
			for at := range entries {
				if entries[at].row.version == candidate.version {
					entries[at].admitted = true
				}
			}
			projected.entries[index.address] = entries
			s.installExclusionProjectionLocked(p, projected)
			p.mu.Unlock()
		}
		conflict, potential, err := s.probeExclusion(ref, binding, bound, candidate, false)
		if err != nil {
			return uniqueConflict{}, err
		}
		if conflict.index != nil {
			return conflict, nil
		}
		if c.Deferrable && potential {
			event := exclusionEvent{parent: ref, index: ObjectRef{owner: ref.owner, Epoch: ref.Epoch, Address: index.address}, constraint: c.OID, row: candidate.row, version: candidate.version}
			if !slices.Contains(s.exclusionModes.events, event) {
				s.exclusionModes.events = append(slices.Clone(s.exclusionModes.events), event)
				if s.foreignActionsSettling {
					s.foreignActionExclusionAdded = true
				}
			}
		}
	}
	return uniqueConflict{}, nil
}
