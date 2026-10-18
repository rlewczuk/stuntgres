package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
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
		remap := func(ref ids.ObjectRef) ids.ObjectRef { return ref }
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
			remap := func(ref ids.ObjectRef) ids.ObjectRef { return ref }
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
func (e *Engine) evolveStageIndexesLocked(stage *CatalogStage, typeParent ids.ObjectAddress, transformations []columnTransformation) error {
	// Preserve the incumbent map walk. In particular an unrelated staged
	// relation must not allocate an adapter/iterator/yield closure.
	provider := dropProvider{base: stage.base, owner: stage.owner, path: stage.dependencyPath, stage: stage, ctx: context.Background()}
	for address, descriptor := range stage.base.state.descriptors {
		index, parent, staged, affected := provider.indexEvolutionInput(address, descriptor)
		if !affected {
			continue
		}
		if err := provider.evolveIndex(address, index, parent, staged, typeParent, transformations); err != nil {
			return err
		}
	}
	return nil
}

// The selector is shared and read-only. It cannot retain its adapter; the value
// receiver of the effect worker below is entered only for an affected parent.
func (provider *dropProvider) indexEvolutionInput(address ids.ObjectAddress, descriptor Descriptor) (*relationDescriptor, *relationDescriptor, bool, bool) {
	index, ok := descriptor.(*relationDescriptor)
	if !ok || index.sqlIndex == nil || provider.removed(address) {
		return nil, nil, false, false
	}
	candidate := provider.replacement(address)
	staged := candidate != nil
	if staged {
		replacement, ok := candidate.(*relationDescriptor)
		if !ok || replacement.sqlIndex == nil {
			return nil, nil, false, false
		}
		index = replacement
	}
	var parent Descriptor
	if provider.removed(index.sqlIndex.parent) {
		parent = nil
	} else if replacement := provider.replacement(index.sqlIndex.parent); replacement != nil {
		parent = replacement
	} else {
		return nil, nil, false, false
	}
	relation, _ := parent.(*relationDescriptor)
	return index, relation, staged, true
}

// Prepared and ordinary adapters share the exact evolution/application worker.
func (provider *dropProvider) evolveIndexes(typeParent ids.ObjectAddress, transformations []columnTransformation) error {
	for address, descriptor := range provider.inputDescriptors() {
		index, parent, staged, affected := provider.indexEvolutionInput(address, descriptor)
		if !affected {
			continue
		}
		if err := provider.evolveIndex(address, index, parent, staged, typeParent, transformations); err != nil {
			return err
		}
	}
	return nil
}

func (provider dropProvider) evolveIndex(address ids.ObjectAddress, index, parent *relationDescriptor, staged bool, typeParent ids.ObjectAddress, transformations []columnTransformation) error {
	oldParent := provider.base.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
	prior, chained := provider.indexLayout(address)
	chained = chained && prior.target == index
	if chained {
		oldParent = prior.targetParent
	}
	var dependentTypes []columnTransformation
	// Layout passes evolve the current candidate without lifecycle repair.
	// Actual TYPE dependencies retain their captured reconstruction semantics.
	if index.sqlIndex.parent == typeParent && (!staged || uniqueIndexTypeAffected(index.sqlIndex, transformations)) {
		dependentTypes = transformations
	}
	next, err := evolveIndexRevision(index, oldParent, parent, dependentTypes, provider.terminalCleanup())
	if err != nil {
		return err
	}
	if next == nil {
		if err := provider.removeObjectLocked(provider.owner.catalog.domain.Ref(provider.base.Epoch(), address), false); err != nil {
			return err
		}
	} else if next != index {
		if err := provider.replace(next); err != nil {
			return err
		}
	}
	if next != nil && next.sqlIndex.ready && next.sqlIndex.live && len(dependentTypes) == 0 && indexLayoutInputsUnchanged(index, oldParent, parent) {
		source, ok := provider.base.state.descriptors[address].(*relationDescriptor)
		if chained {
			source, ok = index, true
		}
		if ok && sameIndexKeySemantics(source, index) {
			if chained && next == prior.target && parent == prior.targetParent {
				return nil
			}
			lineage := indexLayoutLineage{source: source, target: next, sourceParent: oldParent, targetParent: parent}
			if chained {
				lineage.previous = &prior
			}
			provider.recordIndexLayout(address, lineage)
		}
	}
	return nil
}

// Captured heads are immutable; the copied scalar generation/end-root tuple
// fences every build against row publication, even when catalog is unchanged.
func captureIndexStores(storage map[ids.ObjectAddress]*relationStorage, catalog *catalogState) map[ids.ObjectAddress]*relationStorage {
	var captured map[ids.ObjectAddress]*relationStorage
	for _, d := range catalog.descriptors {
		index, ok := d.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		if parent, ok := catalog.descriptors[index.sqlIndex.parent].(*relationDescriptor); ok && parent.partition != nil {
			continue
		}
		if captured == nil {
			captured = make(map[ids.ObjectAddress]*relationStorage)
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
func indexStoresUnchanged(storage, captured map[ids.ObjectAddress]*relationStorage, current, next *catalogState, prepared, previous map[ids.ObjectAddress]*logicalIndexRoot) bool {
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
func (e *Engine) prepareIndexCatalog(evaluation *EvalContext, current, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, roots map[ids.ObjectAddress]*logicalIndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*relationDescriptor, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]indexRootCandidate) (map[ids.ObjectAddress]*logicalIndexRoot, error) {
	return e.prepareIndexPublication(evaluation, current, next, storage, roots, rebuildParents, rebuildIndexes, nil, true, output, auth, runtimeOwners, candidates)
}

func (e *Engine) prepareIndexPublication(evaluation *EvalContext, current, next *catalogState, storage map[ids.ObjectAddress]*relationStorage, roots map[ids.ObjectAddress]*logicalIndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*relationDescriptor, publications []rowPublication, materialize bool, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]indexRootCandidate) (map[ids.ObjectAddress]*logicalIndexRoot, error) {
	ctx := evaluation.Context
	var result map[ids.ObjectAddress]*logicalIndexRoot
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
	if auth.engine != nil {
		local := *evaluation
		local.routineAuthority = auth
		evaluation = &local
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
			result = make(map[ids.ObjectAddress]*logicalIndexRoot, len(roots))
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
					if !rowMaintainsIndex(row, index) || !liveVersion(row, ids.CommitSeq(^uint64(0)), nil, publication.deletion) {
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
				ordering, err := bindIndexKeys(parent, index.sqlIndex)
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
func preserveUnchangedIndexRoots(prepared, previous, live map[ids.ObjectAddress]*logicalIndexRoot) {
	for address, root := range prepared {
		if root == previous[address] {
			prepared[address] = live[address]
		}
	}
}

// Explicit statements must discover derived-key errors before installing private
// rows, not defer them to COMMIT. Admission owns these rows; the captured catalog
// and execution trees are immutable. Finish retains its final publication veto.
func (s *StatementScope) validatePrivateIndexExpressions(catalog *catalogChanges, deletes map[ids.ObjectAddress]*privateDeletionRoot) error {
	if len(s.stage.rows) == 0 {
		return nil
	}
	e := s.op.session.Engine()
	e.mu.Lock()
	if s.txRef.Epoch() != e.catalog.state.epoch {
		e.mu.Unlock()
		return ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(catalog)
	input := s.input
	var programmeAuth AuthorizationView
	if len(s.domainPreparedChecks) != 0 || len(s.routinePrograms) != 0 || len(s.indexBindings) != 0 {
		var err error
		programmeAuth, err = s.writeAuthorizationLocked(e)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		programmeAuth.recaptureCatalog(view)
	}
	for _, descriptor := range view.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || !index.sqlIndex.ready || !index.sqlIndex.live || !index.sqlIndex.hasRuntimeDomainConversion() || len(s.stage.rows[index.sqlIndex.parent]) == 0 {
			continue
		}
		auth := programmeAuth
		var err error
		if auth.engine == nil {
			auth, err = s.writeAuthorizationLocked(e)
		}
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
	if programmeAuth.engine != nil {
		if err := s.readmitPreparedPrograms(programmeAuth); err != nil {
			return err
		}
	}
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
		var preparation indexExecutionPreparation
		parent, ok := view.state.descriptors[index.sqlIndex.parent].(*relationDescriptor)
		if !ok {
			return ErrCatalogReplaced
		}
		if pending {
			var err error
			if index.sqlIndex.method == hashMethod {
				hash, err = bindHashIndex(parent, index.sqlIndex)
			} else {
				ordering, err = bindIndexKeys(parent, index.sqlIndex)
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
			if !liveVersion(row, ids.CommitSeq(^uint64(0)), nil, deletes[index.sqlIndex.parent]) {
				continue
			}
			if output, ok := row.indexOutputs[index.address]; ok && output.matches(index) {
				// The SQL producer already materialized this exact version's
				// membership/key under the captured index semantics.
				continue
			}
			if pending {
				output, err := materializeIndexOutput(&evaluation, &preparation, parent, index, &ordering, &hash, row.values)
				if err != nil {
					return err
				}
				outputs := maps.Clone(row.indexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]indexRowOutput)
				}
				outputs[index.address] = output
				if !copied {
					rows = slices.Clone(rows)
					copied = true
				}
				rows[i].indexOutputs = outputs
				continue
			}
			matches, err := preparation.predicateMatches(&evaluation, parent, index.sqlIndex, row.values, nil, nil)
			if err != nil {
				return err
			}
			if !matches {
				continue
			}
			if err := preparation.prepareOrdinaryKeys(&evaluation, parent, index.sqlIndex); err != nil {
				return err
			}
			evaluation.Row = row.values
			for keyIndex, key := range index.sqlIndex.keys {
				if key.derived == nil {
					continue
				}
				if _, err := preparation.ordinary[keyIndex].Eval(&evaluation); err != nil {
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
