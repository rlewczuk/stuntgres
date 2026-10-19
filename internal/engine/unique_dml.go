package engine

import (
	"errors"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Admission owns private overlays for this statement; committed immutable roots
// are recaptured only after each key grant, including after peer outcome waits.
func (s *StatementScope) uniqueParent(ref ids.ObjectRef) (*catalog.Relation, catalog.Read, error) {
	e := s.op.session.Engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if ref.Domain() != e.catalog.Domain || ref.Epoch != e.catalog.state.Epoch() || ref.Epoch != s.txRef.Epoch() {
		return nil, catalog.Read{}, ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(s.owner.catalog)
	parent, ok := view.Relation(ref.Address)
	if !ok {
		return nil, catalog.Read{}, errors.New("stuntgres: invalid unique parent")
	}
	return parent, view, nil
}

type uniqueBoundKey struct {
	index        *catalog.Relation
	parent       *catalog.Relation
	preparation  indexExecutionPreparation
	ordering     indexOrdering
	grouping     []sqltype.Grouping
	key, scratch []sqltype.Value
}
type uniqueWriteBinding struct {
	ref       ids.ObjectRef
	parent    *catalog.Relation
	catalog   *catalog.State
	keys      []uniqueBoundKey
	keySlots  []int
	typeName  func(sqltype.ID) string
	insertion []indexInsertionBinding
	checked   bool
}

func (s *StatementScope) bindUniqueWrite(ref ids.ObjectRef) (*uniqueWriteBinding, error) {
retry:
	parent, view, err := s.uniqueParent(ref)
	if err != nil {
		return nil, err
	}
	if b := s.uniqueBinding; b != nil && b.ref == ref && b.parent == parent && b.catalog == view.State() {
		return b, nil
	}
	if b := s.indexBindings[ref.Address]; b != nil && b.ref == ref && b.parent == parent && b.catalog == view.State() {
		s.uniqueBinding = b
		return b, nil
	}
	if overlay := s.writeOverlay(); overlay != nil && s.uniqueBinding != nil && s.uniqueBinding.ref == ref {
		overlay.overlays = nil
	}
	b := &uniqueWriteBinding{ref: ref, parent: parent, catalog: view.State()}
	var indexes []*catalog.Relation
	var checkedNonunique []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() || !index.SQLIndex.Live {
			continue
		}
		if index.SQLIndex.Ready && index.SQLIndex.RequiresInsertionOutput() {
			b.checked = true
			if !index.SQLIndex.Unique && !index.SQLIndex.IsExclusion() {
				checkedNonunique = append(checkedNonunique, index)
			}
		}
		if !index.SQLIndex.Unique && !index.SQLIndex.IsExclusion() {
			continue
		}
		// REL18 row-key eligibility is independent of readiness/validity.
		// An expression anywhere excludes the whole index; INCLUDE is payload.
		var slots [32]int
		if len(index.SQLIndex.Keys) > len(slots) {
			return nil, errors.New("stuntgres: invalid unique index key count")
		}
		simple := true
		for at, key := range index.SQLIndex.Keys {
			attribute, node, err := indexCatalogKey(parent, key)
			if err != nil {
				return nil, err
			}
			if node != nil {
				simple = false
				break
			}
			slots[at] = slices.Index(parent.Attributes, attribute)
		}
		if index.SQLIndex.Unique && simple && index.SQLIndex.Predicate == nil {
			for _, slot := range slots[:len(index.SQLIndex.Keys)] {
				if !slices.Contains(b.keySlots, slot) {
					b.keySlots = append(b.keySlots, slot)
				}
			}
		}
		if index.SQLIndex.Ready {
			indexes = append(indexes, index)
		}
	}
	slices.SortFunc(indexes, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	b.keys = make([]uniqueBoundKey, len(indexes))
	for i, index := range indexes {
		bound := &b.keys[i]
		bound.index = index
		bound.parent = parent
		if index.SQLIndex.IsExclusion() {
			bound.ordering, err = bindExclusionOrdering(parent, index.SQLIndex)
		} else {
			bound.ordering, err = bindIndexOrdering(parent, index.SQLIndex)
		}
		if err != nil {
			return nil, err
		}
		n := len(index.SQLIndex.Keys)
		if index.SQLIndex.Method != catalog.GistMethod {
			bound.grouping = make([]sqltype.Grouping, n)
		}
		bound.key, bound.scratch = make([]sqltype.Value, n), make([]sqltype.Value, n)
		for j, key := range index.SQLIndex.Keys {
			usage, usageErr := indexKeyUsage(parent, key)
			if usageErr != nil {
				return nil, usageErr
			}
			if index.SQLIndex.Method == catalog.GistMethod {
				continue
			}
			// network_ops owns typed comparison but not generic grouping. Its
			// claims use the existing comparator-based shared bucket.
			if key.Class.Family == "network_ops" {
				continue
			}
			bound.grouping[j], err = sqltype.BindGrouping(usage)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(checkedNonunique) != 0 {
		b.insertion = make([]indexInsertionBinding, 0, len(b.keys)+len(checkedNonunique))
		for i := range b.keys {
			key := &b.keys[i]
			b.insertion = append(b.insertion, indexInsertionBinding{index: key.index, unique: key})
		}
		for _, index := range checkedNonunique {
			bound := indexInsertionBinding{index: index, parent: parent}
			if index.SQLIndex.Method == catalog.HashMethod {
				bound.hash, err = bindHashIndex(parent, index.SQLIndex)
			} else {
				bound.ordering, err = bindIndexKeys(parent, index.SQLIndex)
			}
			if err != nil {
				return nil, err
			}
			b.insertion = append(b.insertion, bound)
		}
		slices.SortFunc(b.insertion, func(a, b indexInsertionBinding) int {
			if a.index.Address().ObjectID < b.index.Address().ObjectID {
				return -1
			}
			if a.index.Address().ObjectID > b.index.Address().ObjectID {
				return 1
			}
			return 0
		})
	}
	if err := b.inheritConsumedPrograms(s.indexBindings[ref.Address]); err != nil {
		return nil, err
	}
	if b.checked {
		e := s.op.session.Engine()
		e.mu.Lock()
		if e.privateCatalogLocked(s.owner.catalog).State() != view.State() {
			e.mu.Unlock()
			goto retry
		}
		auth, captureErr := s.writeAuthorizationLocked(e)
		if captureErr == nil {
			auth.catalog = view
			auth.recaptureCatalog(view)
			b.typeName, captureErr = e.captureTypeDiagnosticName(auth)
		}
		e.mu.Unlock()
		if captureErr != nil {
			return nil, captureErr
		}
		if err := s.readmitPreparedPrograms(auth); err != nil {
			return nil, err
		}
	}
	s.uniqueBinding = b
	if b.checked {
		if s.indexBindings == nil {
			s.indexBindings = make(map[ids.ObjectAddress]*uniqueWriteBinding)
		}
		s.indexBindings[ref.Address] = b
	}
	return b, nil
}

func (b *uniqueBoundKey) load(row []sqltype.Value, evaluation *EvalContext) ([]sqltype.Value, error) {
	err := b.fillKeys(row, b.key, evaluation)
	return b.key, err
}

func (b *uniqueBoundKey) fillKeys(row, keys []sqltype.Value, evaluation *EvalContext) error {
	if err := b.preparation.prepareKeys(evaluation, b.parent, b.index.SQLIndex, &b.ordering, nil); err != nil {
		return err
	}
	return b.ordering.fillKeys(row, keys, evaluation)
}

func (b *uniqueBoundKey) matches(evaluation *EvalContext, values, key []sqltype.Value) (bool, error) {
	member, err := b.preparation.predicateMatches(evaluation, b.parent, b.index.SQLIndex, values, &b.ordering, nil)
	if err != nil || !member {
		return false, err
	}
	if err := b.fillKeys(values, b.scratch, evaluation); err != nil {
		return false, err
	}
	cmp, err := b.ordering.compareKeys(evaluation.Context, storage.IndexEntry{Keys: key}, storage.IndexEntry{Keys: b.scratch})
	return cmp == 0, err
}

func (s *StatementScope) claimUniqueRow(ref ids.ObjectRef, row storage.RowVersion) error {
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return err
	}
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, statementScope: s, Input: s.input, Output: s.output}
	evaluation.catalog = s.op.session.Engine().catalog.read(binding.catalog)
	if binding.typeName != nil {
		evaluation.Input.TypeDiagnosticName = binding.typeName
	}
	purpose := indexProbeRuntime
	if binding.checked {
		purpose = indexProbeTracked
	}
	for i := range binding.keys {
		bound := &binding.keys[i]
		index := bound.index
		if index.SQLIndex.IsExclusion() {
			c := exclusionDefinition(binding.parent, index)
			if c == nil {
				return ids.ErrCatalogReplaced
			}
			if c.Deferrable || !index.SQLIndex.ExactEqualityClaims() {
				continue
			}
		}
		member, key, err := s.existingUniqueOutput(bound, row, &evaluation, purpose)
		if err != nil {
			return err
		}
		if !member {
			continue
		}
		if !index.SQLIndex.NullsNotDistinct && uniqueHasNull(key) {
			continue
		}
		indexRef := ref
		indexRef.Address = index.Address()
		if err := s.acquireUnique(indexRef, bound, key); err != nil {
			return err
		}
	}
	return nil
}

func uniqueRowExcluded(id ids.RowID, excluded []storage.RowVersion) bool {
	for _, row := range excluded {
		if row.Row == id {
			return true
		}
	}
	return false
}
func uniqueRowDeleted(id ids.RowID, roots ...*privateDeletionRoot) bool {
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
	parent, index *catalog.Relation
	key           []sqltype.Value
	existing      []sqltype.Value
	auth          AuthorizationView
	view          catalog.Read
	schema        string
	row           storage.RowVersion
	committed     bool
	position      int
}

func (c *uniqueConflict) diagnostic(s *StatementScope, ref ids.ObjectRef) error {
	allowed, err := uniqueKeyDetailAllowed(c.auth, ref, c.index.SQLIndex.Keys)
	if err != nil {
		return err
	}
	if c.index.SQLIndex.IsExclusion() {
		return exclusionDiagnostic(s.op.Context(), c.parent, c.index, c.key, c.existing, c.schema, s.output, allowed, false, c.view)
	}
	return uniqueDiagnostic(s.op.Context(), c.parent, c.index, c.key, c.schema, s.output, allowed, false, c.view)
}
func (s *StatementScope) checkUniqueRow(ref ids.ObjectRef, candidate *indexWriteCandidate, excluded []storage.RowVersion, prefix indexWriteBatch) error {
	conflict, err := s.probeUniqueRow(ref, candidate, excluded, prefix, nil, indexProbeRuntime)
	if err != nil {
		return err
	}
	if conflict.index != nil {
		return conflict.diagnostic(s, ref)
	}
	return nil
}
func (s *StatementScope) probeUniqueRow(ref ids.ObjectRef, candidate *indexWriteCandidate, excluded []storage.RowVersion, prefix indexWriteBatch, action *boundInsertConflict, purpose indexProbePurpose) (uniqueConflict, error) {
	row := candidate.values
retry:
	binding, err := s.bindUniqueWrite(ref)
	if err != nil {
		return uniqueConflict{}, err
	}
	if purpose == indexProbeRuntime && binding.checked {
		purpose = indexProbeTracked
	}
	parent := binding.parent
	if len(row) != len(parent.Columns) {
		return uniqueConflict{}, errors.New("stuntgres: invalid staged row width")
	}
	e := s.op.session.Engine()
	ctx := s.op.Context()
	evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, statementScope: s, Input: s.input, Output: s.output}
	evaluation.catalog = e.catalog.read(binding.catalog)
	if binding.typeName != nil {
		evaluation.Input.TypeDiagnosticName = binding.typeName
	}
	if action != nil && action.catalog != binding.catalog {
		view := e.catalog.read(binding.catalog)
		if err := action.selectArbiters(&evaluation, parent, view); err != nil {
			return uniqueConflict{}, err
		}
	}
	length := len(binding.keys)
	if action == nil && binding.insertion != nil {
		length = len(binding.insertion)
	}
	for i := 0; i < length; i++ {
		var bound *uniqueBoundKey
		if action == nil && binding.insertion != nil {
			insertion := &binding.insertion[i]
			if insertion.unique == nil {
				if purpose == indexProbeRevalidation {
					if output, exists := candidate.indexes[insertion.index.Address()]; !exists || !indexOutputMatches(output, insertion.index) {
						return uniqueConflict{}, ids.ErrCatalogReplaced
					}
				} else if err := candidate.materializeInsertion(insertion, &evaluation); err != nil {
					return uniqueConflict{}, err
				}
				continue
			}
			bound = insertion.unique
		} else {
			bound = &binding.keys[i]
		}
		index := bound.index
		if index.SQLIndex.IsExclusion() {
			if conflict, err := s.probeExclusionRow(ref, binding, bound, candidate, excluded, action, purpose); err != nil || conflict.index != nil {
				return conflict, err
			}
			continue
		}
		if action != nil && !slices.Contains(action.selected, index.Address()) {
			continue
		}
		member, key, err := candidate.uniqueOutput(bound, &evaluation, action != nil, purpose)
		if err != nil {
			return uniqueConflict{}, err
		}
		if !member {
			continue
		}
		if !index.SQLIndex.NullsNotDistinct && uniqueHasNull(key) {
			continue
		}
		indexRef := ref
		indexRef.Address = index.Address()
		if err := s.acquireUnique(indexRef, bound, key); err != nil {
			return uniqueConflict{}, err
		}
		e.mu.Lock()
		if ref.Epoch != e.catalog.state.Epoch() {
			e.mu.Unlock()
			return uniqueConflict{}, ids.ErrCatalogReplaced
		}
		current := e.privateCatalogLocked(s.owner.catalog)
		if current.State() != binding.catalog {
			e.mu.Unlock()
			s.uniqueBinding = nil
			goto retry
		}
		currentParent, parentOK := current.Relation(ref.Address)
		currentIndex, indexOK := current.Relation(index.Address())
		if !parentOK || !indexOK || currentParent.Revision() != parent.Revision() || !sameRelationRowShape(parent, currentParent) || currentIndex.Revision() != index.Revision() || !sameIndexKeySemantics(index, currentIndex) {
			e.mu.Unlock()
			return uniqueConflict{}, ids.ErrCatalogReplaced
		}
		parent, index = currentParent, currentIndex
		auth, err := s.writeAuthorizationLocked(e)
		auth.catalog = current
		auth.recaptureCatalog(auth.catalog)
		namespace, _ := current.Namespace(parent.Namespace())
		root := e.storage.IndexRoots[index.Address()]
		if changes := s.owner.catalog; changes != nil {
			if private := changes.uniqueRoots[index.Address()]; private != nil {
				root = private
			}
		}
		var versions []storage.RowVersion
		if action != nil && (action.update != nil || stableIsolation(s.owner.isolation) || index.SQLIndex.HasRuntimeDomainKeyConversion()) {
			if storage := e.privateStorageLocked(s.owner.catalog, ref.Address); storage != nil {
				versions = storage.Versions
			}
		}
		e.mu.Unlock()
		if err != nil {
			return uniqueConflict{}, err
		}
		if namespace == nil {
			return uniqueConflict{}, ids.ErrCatalogReplaced
		}
		if check := s.insertConflict; check != nil {
			if err := auth.checkBinding(bindingForConflict(ref, parent, check), &parser.Insert{}); err != nil {
				return uniqueConflict{}, err
			}
		}
		if root == nil {
			return uniqueConflict{}, errors.New("stuntgres: ready unique index root is missing")
		}
		outcome := uniqueConflict{parent: parent, index: index, key: key, auth: auth, view: current, schema: namespace.Name()}
		lo, hi := 0, len(root.Entries)
		for lo < hi {
			mid := lo + (hi-lo)/2
			cmp, err := bound.ordering.compareKeys(ctx, root.Entries[mid], storage.IndexEntry{Keys: key})
			if err != nil {
				return uniqueConflict{}, err
			}
			if cmp < 0 {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		for at := lo; at < len(root.Entries); at++ {
			entry := root.Entries[at]
			cmp, err := bound.ordering.compareKeys(ctx, entry, storage.IndexEntry{Keys: key})
			if err != nil {
				return uniqueConflict{}, err
			}
			if cmp != 0 {
				break
			}
			if uniqueRowExcluded(entry.Row, excluded) || uniqueRowDeleted(entry.Row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
				continue
			}
			replaced := false
			if c := s.writeOverlay(); c != nil {
				_, replaced = c.affectedRows[entry.Row]
				if !replaced {
					_, replaced = s.insertOwnRow(ref, entry.Row)
				}
			} else {
				for _, own := range s.ownWrites[ref.Address] {
					if own.Row == entry.Row {
						replaced = true
						break
					}
				}
				if !replaced {
					for _, own := range s.stage.rows[ref.Address] {
						if own.Row == entry.Row {
							replaced = true
							break
						}
					}
				}
			}
			if !replaced {
				if action != nil && (action.update != nil || stableIsolation(s.owner.isolation) || index.SQLIndex.HasRuntimeDomainKeyConversion()) {
					head, err := conflictEntryVersion(versions, entry)
					if err != nil {
						return uniqueConflict{}, err
					}
					if purpose != indexProbeRevalidation && index.SQLIndex.HasRuntimeDomainKeyConversion() {
						if err := bound.fillKeys(head.Values, bound.scratch, &evaluation); err != nil {
							return uniqueConflict{}, err
						}
					}
					outcome.row = head
					outcome.position = entry.Position
					outcome.committed = head.Born != 0
				}
				return outcome, nil
			}
		}
		if c := s.writeOverlay(); c != nil {
			own, found, err := s.probeInsertWriteOverlay(ref, bound, &evaluation, key, excluded, purpose)
			if err != nil {
				return uniqueConflict{}, err
			}
			if found {
				if purpose != indexProbeRevalidation && action != nil && index.SQLIndex.HasRuntimeDomainKeyConversion() {
					if err := bound.fillKeys(own.Values, bound.scratch, &evaluation); err != nil {
						return uniqueConflict{}, err
					}
				}
				outcome.row, outcome.position = own, -1
				return outcome, nil
			}
		} else {
			for overlayIndex, overlay := range [2][]storage.RowVersion{s.stage.rows[ref.Address], s.ownWrites[ref.Address]} {
				for _, own := range overlay {
					if overlayIndex == 1 {
						if _, _, replaced := rowByID(s.stage.rows[ref.Address], own.Row, -1); replaced {
							continue
						}
					}
					if uniqueRowExcluded(own.Row, excluded) || uniqueRowDeleted(own.Row, s.ownDeletes[ref.Address], s.stage.deletes[ref.Address]) {
						continue
					}
					equal, err := s.matchesExistingUnique(bound, own, &evaluation, key, purpose)
					if err != nil {
						return uniqueConflict{}, err
					}
					if equal {
						if purpose != indexProbeRevalidation && action != nil && index.SQLIndex.HasRuntimeDomainKeyConversion() {
							if err := bound.fillKeys(own.Values, bound.scratch, &evaluation); err != nil {
								return uniqueConflict{}, err
							}
						}
						outcome.row = own
						outcome.position = -1
						return outcome, nil
					}
				}
			}
		}
		for i := range prefix.values {
			accepted := prefix.candidate(i)
			equal, err := accepted.matchesUnique(bound, &evaluation, key)
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

func conflictEntryVersion(versions []storage.RowVersion, entry storage.IndexEntry) (storage.RowVersion, error) {
	if entry.Position < 0 || entry.Position >= len(versions) {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	head := versions[entry.Position]
	if head.Row != entry.Row || head.Version != entry.Version {
		return storage.RowVersion{}, ids.ErrCatalogReplaced
	}
	return head, nil
}

func (s *StatementScope) uniqueKeyChanged(ref ids.ObjectRef, old, next []sqltype.Value) (bool, error) {
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
