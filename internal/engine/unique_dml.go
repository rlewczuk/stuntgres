package engine

import (
	"errors"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Admission owns private overlays for this statement; committed immutable roots
// are recaptured only after each key grant, including after peer outcome waits.
func (s *StatementScope) uniqueParent(ref ObjectRef) (*relationDescriptor, CatalogRead, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.owner != e.owner || ref.Epoch != e.state.epoch || ref.Epoch != s.txRef.epoch {
		return nil, CatalogRead{}, ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.catalog)
	parent, ok := view.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok {
		return nil, CatalogRead{}, errors.New("stuntgres: invalid unique parent")
	}
	return parent, view, nil
}

type uniqueBoundKey struct {
	index        *relationDescriptor
	ordering     indexOrdering
	grouping     []sqltype.Grouping
	key, scratch []sqltype.Value
}
type uniqueWriteBinding struct {
	ref      ObjectRef
	parent   *relationDescriptor
	catalog  *catalogState
	keys     []uniqueBoundKey
	keySlots []int
}

func (s *StatementScope) bindUniqueWrite(ref ObjectRef) (*uniqueWriteBinding, error) {
	parent, view, err := s.uniqueParent(ref)
	if err != nil {
		return nil, err
	}
	if b := s.uniqueBinding; b != nil && b.ref == ref && b.parent == parent && b.catalog == view.state {
		return b, nil
	}
	if overlay := s.writeOverlay(); overlay != nil && s.uniqueBinding != nil && s.uniqueBinding.ref == ref {
		overlay.overlays = nil
	}
	b := &uniqueWriteBinding{ref: ref, parent: parent, catalog: view.state}
	var indexes []*relationDescriptor
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != parent.address || !index.sqlIndex.live || (!index.sqlIndex.unique && !index.sqlIndex.isExclusion()) {
			continue
		}
		// REL18 row-key eligibility is independent of readiness/validity.
		// An expression anywhere excludes the whole index; INCLUDE is payload.
		var slots [32]int
		if len(index.sqlIndex.keys) > len(slots) {
			return nil, errors.New("stuntgres: invalid unique index key count")
		}
		simple := true
		for at, key := range index.sqlIndex.keys {
			attribute, node, err := indexCatalogKey(parent, key)
			if err != nil {
				return nil, err
			}
			if node != nil {
				simple = false
				break
			}
			slots[at] = slices.Index(parent.attributes, attribute)
		}
		if index.sqlIndex.unique && simple && index.sqlIndex.predicate == nil {
			for _, slot := range slots[:len(index.sqlIndex.keys)] {
				if !slices.Contains(b.keySlots, slot) {
					b.keySlots = append(b.keySlots, slot)
				}
			}
		}
		if index.sqlIndex.ready {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *relationDescriptor) int {
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	b.keys = make([]uniqueBoundKey, len(indexes))
	for i, index := range indexes {
		bound := &b.keys[i]
		bound.index = index
		if index.sqlIndex.isExclusion() {
			bound.ordering, err = bindExclusionOrdering(parent, index.sqlIndex)
		} else {
			bound.ordering, err = bindIndexOrdering(parent, index.sqlIndex)
		}
		if err != nil {
			return nil, err
		}
		n := len(index.sqlIndex.keys)
		bound.grouping = make([]sqltype.Grouping, n)
		bound.key, bound.scratch = make([]sqltype.Value, n), make([]sqltype.Value, n)
		for j, key := range index.sqlIndex.keys {
			usage, usageErr := indexKeyUsage(parent, key)
			if usageErr != nil {
				return nil, usageErr
			}
			// network_ops owns typed comparison but not generic grouping. Its
			// claims use the existing comparator-based shared bucket.
			if key.class.Family == "network_ops" {
				continue
			}
			bound.grouping[j], err = sqltype.BindGrouping(usage)
			if err != nil {
				return nil, err
			}
		}
	}
	s.uniqueBinding = b
	return b, nil
}

func (b *uniqueBoundKey) load(row []sqltype.Value, evaluation *EvalContext) ([]sqltype.Value, error) {
	err := b.ordering.fillKeys(row, b.key, evaluation)
	return b.key, err
}

func (b *uniqueBoundKey) matches(evaluation *EvalContext, values, key []sqltype.Value) (bool, error) {
	member, err := indexPredicateMatches(evaluation, b.index.sqlIndex.predicate, values)
	if err != nil || !member {
		return false, err
	}
	if err := b.ordering.fillKeys(values, b.scratch, evaluation); err != nil {
		return false, err
	}
	cmp, err := b.ordering.compareKeys(evaluation.Context, logicalIndexEntry{keys: key}, logicalIndexEntry{keys: b.scratch})
	return cmp == 0, err
}

func (s *StatementScope) claimUniqueRow(ref ObjectRef, row []sqltype.Value) error {
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return err
	}
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, statementScope: s, Input: s.input, Output: s.output}
	for i := range binding.keys {
		bound := &binding.keys[i]
		index := bound.index
		if index.sqlIndex.isExclusion() {
			c := exclusionDefinition(binding.parent, index)
			if c == nil {
				return ErrCatalogReplaced
			}
			if c.Deferrable {
				continue
			}
		}
		member, err := indexPredicateMatches(&evaluation, index.sqlIndex.predicate, row)
		if err != nil {
			return err
		}
		if !member {
			continue
		}
		key, err := bound.load(row, &evaluation)
		if err != nil {
			return err
		}
		if !index.sqlIndex.nullsNotDistinct && uniqueHasNull(key) {
			continue
		}
		indexRef := ref
		indexRef.Address = index.address
		if err := s.acquireUnique(indexRef, bound, key); err != nil {
			return err
		}
	}
	return nil
}

func uniqueRowExcluded(id RowID, excluded []rowVersion) bool {
	for _, row := range excluded {
		if row.row == id {
			return true
		}
	}
	return false
}
func uniqueRowDeleted(id RowID, roots ...*privateDeletionRoot) bool {
	for _, root := range roots {
		if root != nil {
			if _, ok := root.rows[id]; ok {
				return true
			}
		}
	}
	return false
}

// One real provider probe supplies ordinary violations, omission visibility and
// exact conflict-update identity without scanning committed table payloads.
type uniqueConflict struct {
	parent, index *relationDescriptor
	key           []sqltype.Value
	existing      []sqltype.Value
	auth          AuthorizationView
	view          CatalogRead
	schema        string
	row           rowVersion
	committed     bool
	position      int
}

func (c *uniqueConflict) diagnostic(s *StatementScope, ref ObjectRef) error {
	allowed, err := uniqueKeyDetailAllowed(c.auth, ref, c.index.sqlIndex.keys)
	if err != nil {
		return err
	}
	if c.index.sqlIndex.isExclusion() {
		return exclusionDiagnostic(s.op.Context(), c.parent, c.index, c.key, c.existing, c.schema, s.output, allowed, false, c.view)
	}
	return uniqueDiagnostic(s.op.Context(), c.parent, c.index, c.key, c.schema, s.output, allowed, false, c.view)
}
func (s *StatementScope) checkUniqueRow(ref ObjectRef, row []sqltype.Value, excluded []rowVersion, prefix [][]sqltype.Value) error {
	conflict, err := s.probeUniqueRow(ref, row, excluded, prefix, nil)
	if err != nil {
		return err
	}
	if conflict.index != nil {
		return conflict.diagnostic(s, ref)
	}
	return nil
}
func (s *StatementScope) probeUniqueRow(ref ObjectRef, row []sqltype.Value, excluded []rowVersion, prefix [][]sqltype.Value, action *boundInsertConflict) (uniqueConflict, error) {
retry:
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return uniqueConflict{}, err
	}
	parent := binding.parent
	if len(row) != len(parent.columns) {
		return uniqueConflict{}, errors.New("stuntgres: invalid staged row width")
	}
	e := s.op.session.Engine()
	ctx := s.op.Context()
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, statementScope: s, Input: s.input, Output: s.output}
	if action != nil && action.catalog != binding.catalog {
		view := CatalogRead{owner: e, state: binding.catalog}
		if err := action.selectArbiters(&evaluation, parent, view); err != nil {
			return uniqueConflict{}, err
		}
	}
	if conflict, err := s.probeExclusionRow(ref, row, excluded, action); err != nil || conflict.index != nil {
		return conflict, err
	}
	for i := range binding.keys {
		bound := &binding.keys[i]
		index := bound.index
		if index.sqlIndex.isExclusion() {
			continue
		}
		if action != nil && !slices.Contains(action.selected, index.address) {
			continue
		}
		member, err := indexPredicateMatches(&evaluation, index.sqlIndex.predicate, row)
		if err != nil {
			return uniqueConflict{}, err
		}
		if !member {
			continue
		}
		key, err := bound.load(row, &evaluation)
		if err != nil {
			return uniqueConflict{}, err
		}
		if !index.sqlIndex.nullsNotDistinct && uniqueHasNull(key) {
			continue
		}
		indexRef := ref
		indexRef.Address = index.address
		if err := s.acquireUnique(indexRef, bound, key); err != nil {
			return uniqueConflict{}, err
		}
		e.mu.Lock()
		if ref.Epoch != e.state.epoch {
			e.mu.Unlock()
			return uniqueConflict{}, ErrCatalogReplaced
		}
		current := e.privateCatalogLocked(s.owner.catalog)
		if current.state != binding.catalog {
			e.mu.Unlock()
			s.uniqueBinding = nil
			goto retry
		}
		currentParent, parentOK := current.state.descriptors[ref.Address].(*relationDescriptor)
		currentIndex, indexOK := current.state.descriptors[index.address].(*relationDescriptor)
		if !parentOK || !indexOK || currentParent.revision != parent.revision || !sameRelationRowShape(parent, currentParent) || currentIndex.revision != index.revision || !sameIndexKeySemantics(index, currentIndex) {
			e.mu.Unlock()
			return uniqueConflict{}, ErrCatalogReplaced
		}
		parent, index = currentParent, currentIndex
		auth, err := s.writeAuthorizationLocked(e)
		auth.catalog = current
		namespace, _ := current.state.descriptors[parent.namespace].(*namespaceDescriptor)
		root := e.indexRoots[index.address]
		if changes := s.owner.catalog; changes != nil {
			if private := changes.uniqueRoots[index.address]; private != nil {
				root = private
			}
		}
		var versions []rowVersion
		if action != nil && (action.update != nil || stableIsolation(s.owner.isolation)) {
			if storage := e.privateStorageLocked(s.owner.catalog, ref.Address); storage != nil {
				versions = storage.versions
			}
		}
		e.mu.Unlock()
		if err != nil {
			return uniqueConflict{}, err
		}
		if namespace == nil {
			return uniqueConflict{}, ErrCatalogReplaced
		}
		if check := s.insertConflict; check != nil {
			if err := auth.checkBinding(bindingForConflict(ref, parent, check), &parser.Insert{}); err != nil {
				return uniqueConflict{}, err
			}
		}
		if root == nil {
			return uniqueConflict{}, errors.New("stuntgres: ready unique index root is missing")
		}
		outcome := uniqueConflict{parent: parent, index: index, key: key, auth: auth, view: current, schema: namespace.name}
		lo, hi := 0, len(root.entries)
		for lo < hi {
			mid := lo + (hi-lo)/2
			cmp, err := bound.ordering.compareKeys(ctx, root.entries[mid], logicalIndexEntry{keys: key})
			if err != nil {
				return uniqueConflict{}, err
			}
			if cmp < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		for at := lo; at < len(root.entries); at++ {
			entry := root.entries[at]
			cmp, err := bound.ordering.compareKeys(ctx, entry, logicalIndexEntry{keys: key})
			if err != nil {
				return uniqueConflict{}, err
			}
			if cmp != 0 {
				break
			}
			if uniqueRowExcluded(entry.row, excluded) || uniqueRowDeleted(entry.row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
				continue
			}
			replaced := false
			if c := s.writeOverlay(); c != nil {
				_, replaced = c.affectedRows[entry.row]
				if !replaced {
					_, replaced = s.insertOwnRow(ref, entry.row)
				}
			} else {
				for _, own := range s.ownWrites[ref.Address] {
					if own.row == entry.row {
						replaced = true
						break
					}
				}
				if !replaced {
					for _, own := range s.stage.rows[ref.Address] {
						if own.row == entry.row {
							replaced = true
							break
						}
					}
				}
			}
			if !replaced {
				if action != nil && (action.update != nil || stableIsolation(s.owner.isolation)) {
					head, err := conflictEntryVersion(versions, entry)
					if err != nil {
						return uniqueConflict{}, err
					}
					outcome.row = head
					outcome.position = entry.position
					outcome.committed = head.born != 0
				}
				return outcome, nil
			}
		}
		if c := s.writeOverlay(); c != nil {
			own, found, err := s.probeInsertWriteOverlay(ref, bound, &evaluation, key, excluded)
			if err != nil {
				return uniqueConflict{}, err
			}
			if found {
				outcome.row, outcome.position = own, -1
				return outcome, nil
			}
		} else {
			for overlayIndex, overlay := range [2][]rowVersion{s.stage.rows[ref.Address], s.ownWrites[ref.Address]} {
				for _, own := range overlay {
					if overlayIndex == 1 {
						if _, _, replaced := rowByID(s.stage.rows[ref.Address], own.row, -1); replaced {
							continue
						}
					}
					if uniqueRowExcluded(own.row, excluded) || uniqueRowDeleted(own.row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
						continue
					}
					equal, err := bound.matches(&evaluation, own.values, key)
					if err != nil {
						return uniqueConflict{}, err
					}
					if equal {
						outcome.row = own
						outcome.position = -1
						return outcome, nil
					}
				}
			}
		}
		for _, accepted := range prefix {
			equal, err := bound.matches(&evaluation, accepted, key)
			if err != nil {
				return uniqueConflict{}, err
			}
			if equal {
				return outcome, nil
			}
		}
	}
	return uniqueConflict{}, nil
}

func conflictEntryVersion(versions []rowVersion, entry logicalIndexEntry) (rowVersion, error) {
	if entry.position < 0 || entry.position >= len(versions) {
		return rowVersion{}, ErrCatalogReplaced
	}
	head := versions[entry.position]
	if head.row != entry.row || head.version != entry.version {
		return rowVersion{}, ErrCatalogReplaced
	}
	return head, nil
}

func (s *StatementScope) uniqueKeyChanged(ref ObjectRef, old, next []sqltype.Value) (bool, error) {
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return false, err
	}
	for _, slot := range binding.keySlots {
		if !sqltype.SameDatum(old[slot], next[slot]) {
			return true, nil
		}
	}
	return false, nil
}
