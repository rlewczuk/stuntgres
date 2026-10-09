package engine

import (
	"errors"
	"slices"
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
	constraint   *uniqueConstraint
	ordering     indexOrdering
	grouping     []sqltype.Grouping
	key, scratch []sqltype.Value
}
type uniqueWriteBinding struct {
	ref      ObjectRef
	parent   *relationDescriptor
	keys     []uniqueBoundKey
	keySlots []int
}

func (s *StatementScope) bindUniqueWrite(ref ObjectRef) (*uniqueWriteBinding, error) {
	parent, view, err := s.uniqueParent(ref)
	if err != nil {
		return nil, err
	}
	if b := s.uniqueBinding; b != nil && b.ref == ref && b.parent == parent {
		return b, nil
	}
	b := &uniqueWriteBinding{ref: ref, parent: parent}
	if len(parent.uniqueConstraints) != 0 {
		b.keys = make([]uniqueBoundKey, len(parent.uniqueConstraints))
	}
	for i := range b.keys {
		bound := &b.keys[i]
		c := &parent.uniqueConstraints[i]
		bound.constraint = c
		index, ok := view.state.descriptors[c.Index].(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			return nil, errors.New("stuntgres: unique backing index is missing")
		}
		bound.ordering, err = bindIndexOrdering(parent, index.sqlIndex)
		if err != nil {
			return nil, err
		}
		bound.grouping = make([]sqltype.Grouping, len(c.Attributes))
		bound.key = make([]sqltype.Value, len(c.Attributes))
		bound.scratch = make([]sqltype.Value, len(c.Attributes))
		for _, slot := range bound.ordering.slots {
			if !slices.Contains(b.keySlots, slot) {
				b.keySlots = append(b.keySlots, slot)
			}
		}
		for j, slot := range bound.ordering.slots {
			bound.grouping[j], err = sqltype.BindGrouping(parent.columns[slot].Usage)
			if err != nil {
				return nil, err
			}
		}
	}
	s.uniqueBinding = b
	return b, nil
}

func (b *uniqueBoundKey) load(row []sqltype.Value) []sqltype.Value {
	for i, slot := range b.ordering.slots {
		b.key[i] = row[slot]
	}
	return b.key
}

func (s *StatementScope) claimUniqueRow(ref ObjectRef, row []sqltype.Value) error {
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return err
	}
	for i := range binding.keys {
		bound := &binding.keys[i]
		c := bound.constraint
		key := bound.load(row)
		if !c.NullsNotDistinct && uniqueHasNull(key) {
			continue
		}
		indexRef := ref
		indexRef.Address = c.Index
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

// Exclusions contain only ended accepted-prefix predecessors and the current
// predecessor, never future UPDATE candidates. Prefix rows remain conflicting.
func (s *StatementScope) checkUniqueRow(ref ObjectRef, row []sqltype.Value, excluded []rowVersion, prefix [][]sqltype.Value) error {
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return err
	}
	parent := binding.parent
	if len(row) != len(parent.columns) {
		return errors.New("stuntgres: invalid staged row width")
	}
	e := s.op.session.Engine()
	ctx := s.op.Context()
	for i := range binding.keys {
		bound := &binding.keys[i]
		c := bound.constraint
		ordering := bound.ordering
		key := bound.load(row)
		if !c.NullsNotDistinct && uniqueHasNull(key) {
			continue
		}
		indexRef := ref
		indexRef.Address = c.Index
		if err := s.acquireUnique(indexRef, bound, key); err != nil {
			return err
		}
		e.mu.Lock()
		if ref.Epoch != e.state.epoch {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		current := e.privateCatalogLocked(s.owner.catalog)
		if current.state.descriptors[ref.Address] != parent {
			e.mu.Unlock()
			return ErrCatalogReplaced
		}
		auth, err := e.authorizationLocked(s.op)
		auth.catalog = current
		namespace, _ := current.state.descriptors[parent.namespace].(*namespaceDescriptor)
		root := e.indexRoots[c.Index]
		if changes := s.owner.catalog; changes != nil {
			if private := changes.uniqueRoots[c.Index]; private != nil {
				root = private
			}
		}
		e.mu.Unlock()
		if err != nil {
			return err
		}
		if namespace == nil {
			return ErrCatalogReplaced
		}
		if root == nil {
			return errors.New("stuntgres: unique backing root is missing")
		}
		matches := func(values []sqltype.Value) (bool, error) {
			for j, slot := range ordering.slots {
				bound.scratch[j] = values[slot]
			}
			cmp, err := ordering.compareKeys(ctx, logicalIndexEntry{keys: key}, logicalIndexEntry{keys: bound.scratch})
			return cmp == 0, err
		}
		conflict := false
		// Binary lower bound into the real committed B-tree key range.
		lo, hi := 0, len(root.entries)
		for lo < hi {
			mid := lo + (hi-lo)/2
			cmp, err := ordering.compareKeys(ctx, root.entries[mid], logicalIndexEntry{keys: key})
			if err != nil {
				return err
			}
			if cmp < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		for at := lo; at < len(root.entries); at++ {
			entry := root.entries[at]
			cmp, err := ordering.compareKeys(ctx, entry, logicalIndexEntry{keys: key})
			if err != nil {
				return err
			}
			if cmp != 0 {
				break
			}
			if uniqueRowExcluded(entry.row, excluded) || uniqueRowDeleted(entry.row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
				continue
			}
			replaced := false
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
			if !replaced {
				conflict = true
				break
			}
		}
		for _, overlay := range [][]rowVersion{s.ownWrites[ref.Address], s.stage.rows[ref.Address]} {
			if conflict {
				break
			}
			for _, own := range overlay {
				if uniqueRowExcluded(own.row, excluded) || uniqueRowDeleted(own.row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
					continue
				}
				equal, err := matches(own.values)
				if err != nil {
					return err
				}
				if equal {
					conflict = true
					break
				}
			}
		}
		if !conflict {
			for _, accepted := range prefix {
				equal, err := matches(accepted)
				if err != nil {
					return err
				}
				if equal {
					conflict = true
					break
				}
			}
		}
		if conflict {
			allowed, err := uniqueKeyDetailAllowed(auth, ref, c)
			if err != nil {
				return err
			}
			return uniqueDiagnostic(ctx, parent, c, key, namespace.name, s.output, allowed, false)
		}
	}
	return nil
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
