package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/authorization"
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
	if index.sqlIndex.constraint != nil && !backingConstraintPresent(parent, index) {
		return nil, nil
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
		prior, chained := stage.indexLayouts[a]
		chained = chained && prior.target == index
		if chained {
			oldParent = prior.targetParent
		}
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
		if next != nil && next.sqlIndex.ready && next.sqlIndex.live && len(dependentTypes) == 0 && indexLayoutInputsUnchanged(index, oldParent, relation) {
			source, ok := stage.base.state.descriptors[a].(*relationDescriptor)
			if chained {
				source, ok = index, true
			}
			if ok && sameIndexKeySemantics(source, index) {
				if chained && next == prior.target && relation == prior.targetParent {
					continue
				}
				if stage.indexLayouts == nil {
					stage.indexLayouts = make(map[ObjectAddress]indexLayoutLineage)
				}
				lineage := indexLayoutLineage{source: source, target: next, sourceParent: oldParent, targetParent: relation}
				if chained {
					lineage.previous = &prior
				}
				stage.indexLayouts[a] = lineage
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
		if parent, ok := catalog.descriptors[index.sqlIndex.parent].(*relationDescriptor); ok && parent.partition != nil {
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
		if parent, ok := next.descriptors[a].(*relationDescriptor); ok && parent.partition != nil {
			continue
		}
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
func (e *Engine) prepareIndexCatalog(evaluation *EvalContext, current, next *catalogState, storage map[ObjectAddress]*relationStorage, roots map[ObjectAddress]*logicalIndexRoot, rebuildParents map[ObjectAddress]bool, rebuildIndexes map[ObjectAddress]*relationDescriptor, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ObjectAddress]authorization.PrincipalRef, candidates map[ObjectAddress]indexRootCandidate) (map[ObjectAddress]*logicalIndexRoot, error) {
	return e.prepareIndexPublication(evaluation, current, next, storage, roots, rebuildParents, rebuildIndexes, nil, true, output, auth, runtimeOwners, candidates)
}

func (e *Engine) prepareIndexPublication(evaluation *EvalContext, current, next *catalogState, storage map[ObjectAddress]*relationStorage, roots map[ObjectAddress]*logicalIndexRoot, rebuildParents map[ObjectAddress]bool, rebuildIndexes map[ObjectAddress]*relationDescriptor, publications []rowPublication, materialize bool, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ObjectAddress]authorization.PrincipalRef, candidates map[ObjectAddress]indexRootCandidate) (map[ObjectAddress]*logicalIndexRoot, error) {
	ctx := evaluation.Context
	var result map[ObjectAddress]*logicalIndexRoot
	catalog := CatalogRead{owner: e, state: next}
	evaluation.catalog, evaluation.Output = catalog, output
	if auth == nil {
		// Trusted replacement has catalog identity but no SQL principal.
		auth = &AuthorizationView{catalog: catalog}
	} else {
		disclosure := *auth
		disclosure.catalog = catalog
		disclosure.recaptureCatalog(disclosure.catalog)
		auth = &disclosure
	}
	for a, descriptor := range next.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		parent := next.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		owner, runtime := runtimeOwners[parent.address]
		indexAuth := auth
		if runtime && auth.engine != nil {
			if _, err := auth.roles.Descriptor(owner); err != nil {
				return nil, err
			}
			disclosure := *auth
			disclosure.state.CurrentUser = owner
			indexAuth = &disclosure
		}
		if parent.partition != nil {
			if current.descriptors[a] != index {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if err := validateIndexDefinition(catalog, index); err != nil {
				return nil, err
			}
			if roots[a] != nil {
				return nil, fmt.Errorf("stuntgres: virtual index has physical storage")
			}
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
		candidate, produced := candidates[a]
		if produced {
			if candidate.root == nil || !candidate.matchesIndex(index, storage[parent.address]) {
				return nil, CatalogConflict{}
			}
			root = candidate.root
		}
		old, _ := current.descriptors[a].(*relationDescriptor)
		if roots != nil && root == nil && old != nil && old.sqlIndex.ready && !intent {
			return nil, fmt.Errorf("stuntgres: ready index root is missing")
		}
		rebuild := !produced && (root == nil || old == nil || intent || !sameIndexKeySemantics(old, index))
		// Generic parent shape changes cannot heal invalid indexes. Supplied
		// phase roots and ready-invalid maintenance still use the same core.
		if old != nil && !index.sqlIndex.valid && rebuildIndexes[a] == nil && !runtime {
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
		if index.sqlIndex.hasRuntimeDomainConversion() && indexAuth.engine != nil {
			needsEvaluation := rebuild
			for _, publication := range publications {
				if publication.address != parent.address {
					continue
				}
				for _, row := range publication.writes {
					if !rowMaintainsIndex(row, index) || !liveVersion(row, CommitSeq(^uint64(0)), nil, publication.deletion) {
						continue
					}
					if output, ok := row.indexOutputs[a]; !ok || !output.matches(index) {
						needsEvaluation = true
						break
					}
				}
				break
			}
			if needsEvaluation {
				var err error
				evaluation.Input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(*indexAuth)
				if err != nil {
					return nil, err
				}
			}
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
				root, err = buildHashIndexRoot(evaluation, parent, index.sqlIndex, storage[parent.address], materialize || index.sqlIndex.isExclusion(), nil)
				if err != nil {
					return nil, err
				}
			}
			for i := range publications {
				if publications[i].address != parent.address {
					continue
				}
				reconstruct := !produced && (rebuildParents[parent.address] || old == nil) && index.sqlIndex.valid
				root, err = mergeHashIndexRoot(evaluation, parent, index, root, &publications[i], reconstruct, materialize || index.sqlIndex.isExclusion())
				if err != nil {
					return nil, err
				}
				break
			}
			if index.sqlIndex.isExclusion() {
				namespace := next.descriptors[parent.namespace].(*namespaceDescriptor)
				if err := validateExclusionRoot(ctx, parent, index, root, namespace.name, output, indexAuth, rebuild && !runtime); err != nil {
					return nil, err
				}
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
			entries, err = buildIndexEntries(evaluation, parent, index.sqlIndex, storage[parent.address], nil)
			if err != nil {
				return nil, err
			}
			root = nil
			if index.sqlIndex.unique || index.sqlIndex.isExclusion() {
				// Namespace metadata belongs to integrity diagnostics, not ordinary
				// key construction. Replacement also accepts reduced validated
				// parent/index catalogues for ordinary nonintegrity roots.
				namespace := next.descriptors[parent.namespace].(*namespaceDescriptor)
				ordering, err := bindIndexOrdering(parent, index.sqlIndex)
				if err != nil {
					return nil, err
				}
				if err := validateUniqueEntries(ctx, parent, index, entries, namespace.name, output, indexAuth, !runtime, ordering); err != nil {
					return nil, err
				}
			}
		}
		for i := range publications {
			if publications[i].address != parent.address {
				continue
			}
			schema := ""
			if index.sqlIndex.unique || index.sqlIndex.isExclusion() {
				schema = next.descriptors[parent.namespace].(*namespaceDescriptor).name
			}
			reconstruct := !produced && (rebuildParents[parent.address] || old == nil) && index.sqlIndex.valid
			merged, err := mergeIndexRoot(evaluation, parent, index, entries, &publications[i], reconstruct, materialize, false, schema, output, indexAuth)
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
	if a == nil || b == nil || a.address != b.address {
		return false
	}
	if a == b {
		return true
	}
	if !sameIndexDefinitionSemantics(a.sqlIndex, b.sqlIndex) || len(a.columns) != len(b.columns) {
		return false
	}
	for i, column := range a.columns {
		if column.Usage != b.columns[i].Usage {
			return false
		}
	}
	return true
}

func sameIndexDefinitionSemantics(a, b *indexDefinition) bool {
	return a != nil && b != nil && a.method == b.method && a.parent == b.parent && slices.Equal(a.keys, b.keys) && slices.Equal(a.include, b.include) && slices.Equal(a.exclusion, b.exclusion) && a.unique == b.unique && a.nullsNotDistinct == b.nullsNotDistinct && a.predicate == b.predicate
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
	input := s.input
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || !index.sqlIndex.ready || !index.sqlIndex.live || !index.sqlIndex.hasRuntimeDomainConversion() || len(s.stage.rows[index.sqlIndex.parent]) == 0 {
			continue
		}
		auth, err := s.writeAuthorizationLocked(e)
		if err == nil {
			auth.catalog = view
			auth.recaptureCatalog(view)
			input.TypeDiagnosticName, err = e.captureTypeDiagnosticName(auth)
		}
		if err != nil {
			e.mu.Unlock()
			return err
		}
		break
	}
	e.mu.Unlock()
	evaluation := EvalContext{Context: s.op.Context(), Scope: 1, Frame: 1, statementScope: s, Input: input, Output: s.output, catalog: view}
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
		pending := index.sqlIndex.hasRuntimeDomainConversion()
		var ordering indexOrdering
		var hash hashIndexBinding
		if pending {
			parent, ok := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
			if !ok {
				return ErrCatalogReplaced
			}
			var err error
			if index.sqlIndex.method == hashMethod {
				hash, err = bindHashIndex(parent, index.sqlIndex)
			} else {
				ordering, err = bindIndexOrdering(parent, index.sqlIndex)
			}
			if err != nil {
				return err
			}
		}
		copied := false
		for i, row := range rows {
			if !rowMaintainsIndex(row, index) {
				continue
			}
			if !liveVersion(row, CommitSeq(^uint64(0)), nil, deletes[index.sqlIndex.parent]) {
				continue
			}
			if output, ok := row.indexOutputs[index.address]; ok && output.matches(index) {
				// The SQL producer already materialized this exact version's
				// membership/key under the captured index semantics.
				continue
			}
			if pending {
				output, err := materializeIndexOutput(&evaluation, index, ordering, hash, row.values)
				if err != nil {
					return err
				}
				outputs := maps.Clone(row.indexOutputs)
				if outputs == nil {
					outputs = make(map[ObjectAddress]indexRowOutput)
				}
				outputs[index.address] = output
				if !copied {
					rows = slices.Clone(rows)
					copied = true
				}
				rows[i].indexOutputs = outputs
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
		if copied {
			s.stage.rows[index.sqlIndex.parent] = rows
		}
	}
	return nil
}
