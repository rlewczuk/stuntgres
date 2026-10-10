package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqltype"
)

// Return a detached descriptor when a dependent attribute TYPE or schema/owner
// changes. TYPE reconstructs the whole definition using OLD type defaults,
// matching PostgreSQL's captured pg_get_indexdef rather than original spelling.
func evolveIndex(index, oldParent, parent *relationDescriptor, transformations []columnTransformation) (*relationDescriptor, error) {
	return evolveIndexRevision(index, oldParent, parent, transformations, false)
}

func evolveIndexRevision(index, oldParent, parent *relationDescriptor, transformations []columnTransformation, terminal bool) (*relationDescriptor, error) {
	if parent == nil {
		return nil, nil
	}
	if index.sqlIndex.constraint != nil {
		found := false
		for _, c := range parent.uniqueConstraints {
			if c.Index == index.address && c.OID == index.sqlIndex.constraint.OID {
				found = true
				break
			}
		}
		if !found {
			return nil, nil
		}
	}
	definition := index.sqlIndex
	reconstruct := false
	dependentAttribute := func(attribute int32) bool {
		if !parent.hasAttribute(attribute) {
			return false
		}
		for _, transformation := range transformations {
			if transformation.attribute == attribute {
				reconstruct = true
				break
			}
		}
		return true
	}
	for i, key := range index.sqlIndex.keys {
		if key.derived == nil {
			if !dependentAttribute(key.attribute) {
				return nil, nil
			}
			continue
		}
		var slots map[int32]int
		for _, dependency := range key.derived.dependencies {
			a := dependency.Address
			if a.ClassID == parent.address.ClassID && a.ObjectID == parent.address.ObjectID && a.SubID != 0 {
				if !dependentAttribute(a.SubID) {
					return nil, nil
				}
				if !slices.Equal(key.derived.attributes, parent.attributes) && slots == nil {
					slots = make(map[int32]int, len(parent.attributes))
					for slot, id := range parent.attributes {
						slots[id] = slot
					}
				}
			}
		}
		if slots == nil {
			continue
		}
		remap := func(ref ObjectRef) ObjectRef { return ref }
		node, err := remapBoundExpression(key.derived.definition, remap, slots)
		if err != nil {
			return nil, err
		}
		execution, err := remapBoundExpression(key.derived.execution, remap, slots)
		if err != nil {
			return nil, err
		}
		if definition == index.sqlIndex {
			copy := *definition
			copy.keys = append([]indexKey(nil), copy.keys...)
			definition = &copy
		}
		derived := *key.derived
		derived.definition, derived.execution = node, execution
		derived.attributes = parent.attributes
		definition.keys[i].derived = &derived
	}
	if predicate := definition.predicate; predicate != nil {
		for _, dependency := range predicate.dependencies {
			a := dependency.Address
			if a.ClassID == parent.address.ClassID && a.ObjectID == parent.address.ObjectID && a.SubID != 0 && !dependentAttribute(a.SubID) {
				return nil, nil
			}
		}
		if !slices.Equal(predicate.attributes, parent.attributes) {
			slots := make(map[int32]int, len(parent.attributes))
			for slot, id := range parent.attributes {
				slots[id] = slot
			}
			remap := func(ref ObjectRef) ObjectRef { return ref }
			node, err := remapBoundExpression(predicate.definition, remap, slots)
			if err != nil {
				return nil, err
			}
			execution, err := remapBoundExpression(predicate.execution, remap, slots)
			if err != nil {
				return nil, err
			}
			if definition == index.sqlIndex {
				copy := *definition
				copy.keys = append([]indexKey(nil), copy.keys...)
				definition = &copy
			}
			copy := *predicate
			copy.definition, copy.execution, copy.attributes = node, execution, parent.attributes
			definition.predicate = &copy
		}
	}
	for _, attribute := range definition.include {
		if !dependentAttribute(attribute) {
			return nil, nil
		}
	}
	if reconstruct {
		if definition == index.sqlIndex {
			next := *definition
			next.keys = append([]indexKey(nil), definition.keys...)
			definition = &next
		}
		definition.ready, definition.valid, definition.live = true, true, true
		for i, key := range definition.keys {
			var oldUsage, newUsage sqltype.Usage
			if key.derived != nil {
				oldUsage, newUsage = key.derived.definition.ResultUsage(), key.derived.execution.ResultUsage()
			} else {
				oldUsage = oldParent.columns[slices.Index(oldParent.attributes, key.attribute)].Usage
				newUsage = parent.columns[slices.Index(parent.attributes, key.attribute)].Usage
			}
			definition.keys[i] = reconstructIndexKey(key, oldUsage, newUsage, definition.method)
		}
	}
	if err := bindIndexProvider(parent, definition); err != nil {
		return nil, err
	}
	changed := definition != index.sqlIndex || index.namespace != parent.namespace || index.owner != parent.owner
	for i, key := range definition.keys {
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		changed = changed || index.columns[i].Usage != usage
	}
	if !changed {
		return index, nil
	}
	clone := *index
	clone.namespace, clone.owner = parent.namespace, parent.owner
	clone.sqlIndex = definition
	clone.columns = append([]ColumnDescriptor(nil), index.columns...)
	for i, key := range definition.keys {
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		clone.columns[i].Usage = usage
	}
	for i, attribute := range definition.include {
		slot := slices.Index(parent.attributes, attribute)
		clone.columns[len(definition.keys)+i].Usage = parent.columns[slot].Usage
	}
	clone.revision++
	if clone.revision == 0 {
		if !terminal {
			return nil, fmt.Errorf("stuntgres: index revision exhausted")
		}
		clone.revision = index.revision
	}
	return &clone, nil
}
func (e *Engine) evolveStageIndexesLocked(stage *CatalogStage, typeParent ObjectAddress, transformations []columnTransformation) error {
	for a, d := range stage.base.state.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		if _, removed := stage.removals[a]; removed {
			continue
		}
		candidate, staged := stage.descriptors[a]
		if staged {
			replacement, ok := candidate.(*relationDescriptor)
			if !ok || replacement.sqlIndex == nil {
				continue
			}
			index = replacement
		}
		var parent Descriptor
		if _, removed := stage.removals[index.sqlIndex.parent]; removed {
			parent = nil
		} else if replacement, changed := stage.descriptors[index.sqlIndex.parent]; changed {
			parent = replacement
		} else {
			continue
		}
		relation, _ := parent.(*relationDescriptor)
		oldParent := stage.base.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		var dependentTypes []columnTransformation
		// Layout passes evolve the current candidate without lifecycle repair.
		// Actual TYPE dependencies, including rebound predicates/keys/INCLUDE,
		// retain reconstruction and normalization against their captured Usage.
		if index.sqlIndex.parent == typeParent && (!staged || uniqueIndexTypeAffected(index.sqlIndex, transformations)) {
			dependentTypes = transformations
		}
		next, err := evolveIndexRevision(index, oldParent, relation, dependentTypes, stage.terminalCleanup)
		if err != nil {
			return err
		}
		if next == nil {
			if err := stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: a}, false); err != nil {
				return err
			}
		} else if next != index {
			if err := stage.Replace(next); err != nil {
				return err
			}
		}
	}
	return nil
}

// Captured heads are immutable; the copied scalar generation/end-root tuple
// fences every build against row publication, even when catalog is unchanged.
func captureIndexStores(storage map[ObjectAddress]*relationStorage, catalog *catalogState) map[ObjectAddress]*relationStorage {
	var captured map[ObjectAddress]*relationStorage
	for _, d := range catalog.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		if captured == nil {
			captured = make(map[ObjectAddress]*relationStorage)
		}
		a := index.sqlIndex.parent
		if captured[a] != nil {
			continue
		}
		if store := storage[a]; store != nil {
			copy := *store
			copy.indexSource = store
			captured[a] = &copy
		}
	}
	return captured
}
func indexStoresUnchanged(storage, captured map[ObjectAddress]*relationStorage, current, next *catalogState, prepared, previous map[ObjectAddress]*logicalIndexRoot) bool {
	if len(prepared) == 0 && len(captured) == 0 {
		return true
	}
	for address, d := range next.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || prepared != nil && prepared[address] == previous[address] {
			continue
		}
		a := index.sqlIndex.parent
		live, old := storage[a], captured[a]
		if current.descriptors[a] == nil {
			if live != nil || old != nil {
				return false
			}
			continue
		}
		if live == nil || old == nil || live.generation != old.generation || live.ends != old.ends || len(live.versions) != len(old.versions) {
			return false
		}
	}
	return true
}

// Constructors poll cancellation while doing index work; the publication owner
// performs the final veto. An empty/unchanged set must not add an Err callback
// between the owner's established successful observation and installation.
func (e *Engine) prepareIndexCatalog(ctx context.Context, current, next *catalogState, storage map[ObjectAddress]*relationStorage, roots map[ObjectAddress]*logicalIndexRoot, rebuildParents map[ObjectAddress]bool, rebuildIndexes map[ObjectAddress]*relationDescriptor, output sqltype.OutputEnvironment, auth *AuthorizationView) (map[ObjectAddress]*logicalIndexRoot, error) {
	return e.prepareIndexPublication(ctx, current, next, storage, roots, rebuildParents, rebuildIndexes, nil, true, output, auth)
}

func (e *Engine) prepareIndexPublication(ctx context.Context, current, next *catalogState, storage map[ObjectAddress]*relationStorage, roots map[ObjectAddress]*logicalIndexRoot, rebuildParents map[ObjectAddress]bool, rebuildIndexes map[ObjectAddress]*relationDescriptor, publications []rowPublication, materialize bool, output sqltype.OutputEnvironment, auth *AuthorizationView) (map[ObjectAddress]*logicalIndexRoot, error) {
	var result map[ObjectAddress]*logicalIndexRoot
	catalog := CatalogRead{owner: e, state: next}
	if auth == nil {
		// Trusted replacement has catalog identity but no SQL principal.
		auth = &AuthorizationView{catalog: catalog}
	} else {
		disclosure := *auth
		disclosure.catalog = catalog
		auth = &disclosure
	}
	for a, descriptor := range next.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		intent := rebuildParents[index.sqlIndex.parent] || rebuildIndexes[a] != nil
		if !index.sqlIndex.ready && rebuildIndexes[a] == nil {
			if current.descriptors[a] != index {
				if err := validateIndexDefinition(catalog, index); err != nil {
					return nil, err
				}
			}
			continue
		}
		if materialize && result == nil {
			result = make(map[ObjectAddress]*logicalIndexRoot, len(roots))
		}
		root := roots[a]
		old, _ := current.descriptors[a].(*relationDescriptor)
		parent := next.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if roots != nil && root == nil && old != nil && old.sqlIndex.ready && !intent {
			return nil, fmt.Errorf("stuntgres: ready index root is missing")
		}
		rebuild := root == nil || old == nil || intent || !sameIndexKeySemantics(old, index)
		// Generic parent shape changes cannot heal invalid indexes. Supplied
		// phase roots and ready-invalid maintenance still use the same core.
		if old != nil && !index.sqlIndex.valid && rebuildIndexes[a] == nil {
			if root == nil {
				return nil, fmt.Errorf("stuntgres: ready index root is missing")
			}
			if old != index {
				if err := validateIndexDefinition(catalog, index); err != nil {
					return nil, err
				}
			}
			rebuild = false
		}
		var entries []logicalIndexEntry
		if root != nil {
			entries = root.entries
		}
		if index.sqlIndex.method == hashMethod {
			var err error
			if rebuild {
				if err := validateIndexDefinition(CatalogRead{owner: e, state: next}, index); err != nil {
					return nil, err
				}
				root, err = buildHashIndexRoot(ctx, parent, index.sqlIndex, storage[parent.address], materialize)
				if err != nil {
					return nil, err
				}
			}
			for i := range publications {
				if publications[i].address != parent.address {
					continue
				}
				reconstruct := rebuildParents[parent.address] && index.sqlIndex.valid
				root, err = mergeHashIndexRoot(ctx, parent, index, root, &publications[i], reconstruct, materialize)
				if err != nil {
					return nil, err
				}
				break
			}
			if materialize {
				result[a] = root
			}
			continue
		}
		if rebuild {
			if err := validateIndexDefinition(catalog, index); err != nil {
				return nil, err
			}
			// Required ordering/uniqueness scratch is not an installation
			// root. PREPARE returns neither a root wrapper nor a root map.
			var err error
			entries, err = buildIndexEntries(ctx, parent, index.sqlIndex, storage[parent.address])
			if err != nil {
				return nil, err
			}
			root = nil
			if index.sqlIndex.unique {
				// Namespace metadata belongs to UNIQUE diagnostics, not ordinary
				// key construction. Replacement also accepts reduced validated
				// parent/index catalogues for nonunique roots.
				namespace := next.descriptors[parent.namespace].(*namespaceDescriptor)
				ordering, err := bindIndexOrdering(parent, index.sqlIndex)
				if err != nil {
					return nil, err
				}
				if err := validateUniqueEntries(ctx, parent, index, entries, namespace.name, output, auth, true, ordering); err != nil {
					return nil, err
				}
			}
		}
		for i := range publications {
			if publications[i].address != parent.address {
				continue
			}
			schema := ""
			if index.sqlIndex.unique {
				schema = next.descriptors[parent.namespace].(*namespaceDescriptor).name
			}
			reconstruct := rebuildParents[parent.address] && index.sqlIndex.valid
			merged, err := mergeIndexRoot(ctx, parent, index, entries, &publications[i], reconstruct, materialize, schema, output, auth)
			if err != nil {
				return nil, err
			}
			if merged != nil {
				root = merged
			}
			break
		}
		if materialize {
			if root == nil {
				root = newLogicalIndexRoot(index.sqlIndex, entries)
			}
			result[a] = root
		}
	}
	return result, nil
}

// A backing-constraint rename changes a wrapper, not key/provider semantics.
// Keep roots only when method, key/null-policy and predicate identities are unchanged.
func sameIndexKeySemantics(a, b *relationDescriptor) bool {
	if a == b {
		return true
	}
	if a.sqlIndex == nil || b.sqlIndex == nil || a.sqlIndex.method != b.sqlIndex.method || a.sqlIndex.parent != b.sqlIndex.parent || !slices.Equal(a.sqlIndex.keys, b.sqlIndex.keys) || !slices.Equal(a.sqlIndex.include, b.sqlIndex.include) || len(a.columns) != len(b.columns) {
		return false
	}
	for i, column := range a.columns {
		if column.Usage != b.columns[i].Usage {
			return false
		}
	}
	return a.sqlIndex.unique == b.sqlIndex.unique && a.sqlIndex.nullsNotDistinct == b.sqlIndex.nullsNotDistinct && a.sqlIndex.predicate == b.sqlIndex.predicate
}

// Only changed roots require a storage fence. Unchanged siblings may have
// committed while preparation ran; retain their newest immutable roots.
func preserveUnchangedIndexRoots(prepared, previous, live map[ObjectAddress]*logicalIndexRoot) {
	for address, root := range prepared {
		if root == previous[address] {
			prepared[address] = live[address]
		}
	}
}

// Explicit statements must discover derived-key errors before installing private
// rows, not defer them to COMMIT. Admission owns these rows; the captured catalog
// and execution trees are immutable. Finish retains its final publication veto.
func (s *StatementScope) validatePrivateIndexExpressions(catalog *catalogChanges, deletes map[ObjectAddress]*privateDeletionRoot) error {
	if len(s.stage.rows) == 0 {
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.epoch != e.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(catalog)
	e.mu.Unlock()
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1}
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || !index.sqlIndex.ready || !index.sqlIndex.live {
			continue
		}
		rows := s.stage.rows[index.sqlIndex.parent]
		if len(rows) == 0 {
			continue
		}
		if index.sqlIndex.predicate == nil {
			derived := false
			for _, key := range index.sqlIndex.keys {
				derived = derived || key.derived != nil
			}
			if !derived {
				continue
			}
		}
		for _, row := range rows {
			if !rowMaintainsIndex(row, index) {
				continue
			}
			if !liveVersion(row, CommitSeq(^uint64(0)), nil, deletes[index.sqlIndex.parent]) {
				continue
			}
			matches, err := indexPredicateMatches(&evaluation, index.sqlIndex.predicate, row.values)
			if err != nil {
				return err
			}
			if !matches {
				continue
			}
			evaluation.Row = row.values
			for _, key := range index.sqlIndex.keys {
				if key.derived == nil {
					continue
				}
				if _, err := key.derived.execution.Eval(&evaluation); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
