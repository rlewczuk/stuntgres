package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Return a detached descriptor when a dependent attribute TYPE or schema/owner
// changes. TYPE reconstructs the whole definition using OLD type defaults,
// matching PostgreSQL's captured pg_get_indexdef rather than original spelling.
func evolveIndex(index, oldParent, parent *catalog.Relation, transformations []columnTransformation) (*catalog.Relation, error) {
	return evolveIndexRevision(index, oldParent, parent, transformations, false)
}

func evolveIndexRevision(index, oldParent, parent *catalog.Relation, transformations []columnTransformation, terminal bool) (*catalog.Relation, error) {
	if parent == nil {
		return nil, nil
	}
	if index.SQLIndex.Constraint != nil && !backingConstraintPresent(parent, index) {
		return nil, nil
	}
	definition := index.SQLIndex
	reconstruct := false
	dependentAttribute := func(attribute int32) bool {
		if !parent.HasAttribute(attribute) {
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
	for i, key := range index.SQLIndex.Keys {
		if key.Derived == nil {
			if !dependentAttribute(key.Attribute) {
				return nil, nil
			}
			continue
		}
		var slots map[int32]int
		for _, dependency := range key.Derived.Dependencies {
			a := dependency.Address
			if a.ClassID == parent.Address().ClassID && a.ObjectID == parent.Address().ObjectID && a.SubID != 0 {
				if !dependentAttribute(a.SubID) {
					return nil, nil
				}
				if !slices.Equal(key.Derived.Attributes, parent.Attributes) && slots == nil {
					slots = make(map[int32]int, len(parent.Attributes))
					for slot, id := range parent.Attributes {
						slots[id] = slot
					}
				}
			}
		}
		if slots == nil {
			continue
		}
		remap := func(ref ids.ObjectRef) ids.ObjectRef { return ref }
		node, err := remapBoundExpression(key.Derived.Definition, remap, slots)
		if err != nil {
			return nil, err
		}
		execution, err := remapBoundExpression(key.Derived.Execution, remap, slots)
		if err != nil {
			return nil, err
		}
		if definition == index.SQLIndex {
			copy := *definition
			copy.Keys = append([]catalog.IndexKey(nil), copy.Keys...)
			definition = &copy
		}
		derived := *key.Derived
		derived.Definition, derived.Execution = node, execution
		derived.Attributes = parent.Attributes
		definition.Keys[i].Derived = &derived
	}
	if predicate := definition.Predicate; predicate != nil {
		for _, dependency := range predicate.Dependencies {
			a := dependency.Address
			if a.ClassID == parent.Address().ClassID && a.ObjectID == parent.Address().ObjectID && a.SubID != 0 && !dependentAttribute(a.SubID) {
				return nil, nil
			}
		}
		if !slices.Equal(predicate.Attributes, parent.Attributes) {
			slots := make(map[int32]int, len(parent.Attributes))
			for slot, id := range parent.Attributes {
				slots[id] = slot
			}
			remap := func(ref ids.ObjectRef) ids.ObjectRef { return ref }
			node, err := remapBoundExpression(predicate.Definition, remap, slots)
			if err != nil {
				return nil, err
			}
			execution, err := remapBoundExpression(predicate.Execution, remap, slots)
			if err != nil {
				return nil, err
			}
			if definition == index.SQLIndex {
				copy := *definition
				copy.Keys = append([]catalog.IndexKey(nil), copy.Keys...)
				definition = &copy
			}
			copy := *predicate
			copy.Definition, copy.Execution, copy.Attributes = node, execution, parent.Attributes
			definition.Predicate = &copy
		}
	}
	for _, attribute := range definition.Include {
		if !dependentAttribute(attribute) {
			return nil, nil
		}
	}
	if reconstruct {
		if definition == index.SQLIndex {
			next := *definition
			next.Keys = append([]catalog.IndexKey(nil), definition.Keys...)
			definition = &next
		}
		definition.Ready, definition.Valid, definition.Live = true, true, true
		for i, key := range definition.Keys {
			var oldUsage, newUsage sqltype.Usage
			if key.Derived != nil {
				oldUsage, newUsage = key.Derived.Definition.ResultUsage(), key.Derived.Execution.ResultUsage()
			} else {
				oldUsage = oldParent.Columns[slices.Index(oldParent.Attributes, key.Attribute)].Usage
				newUsage = parent.Columns[slices.Index(parent.Attributes, key.Attribute)].Usage
			}
			definition.Keys[i] = reconstructIndexKey(key, oldUsage, newUsage, definition.Method)
		}
	}
	if err := bindIndexProvider(parent, definition); err != nil {
		return nil, err
	}
	changed := definition != index.SQLIndex || index.Namespace() != parent.Namespace() || index.Owner != parent.Owner
	for i, key := range definition.Keys {
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		changed = changed || index.Columns[i].Usage != usage
	}
	if !changed {
		return index, nil
	}
	clone := *index
	clone.ObjectNamespace, clone.Owner = parent.Namespace(), parent.Owner
	clone.SQLIndex = definition
	clone.Columns = append([]catalog.Column(nil), index.Columns...)
	for i, key := range definition.Keys {
		usage, err := indexKeyUsage(parent, key)
		if err != nil {
			return nil, err
		}
		clone.Columns[i].Usage = usage
	}
	for i, attribute := range definition.Include {
		slot := slices.Index(parent.Attributes, attribute)
		clone.Columns[len(definition.Keys)+i].Usage = parent.Columns[slot].Usage
	}
	clone.ObjectRevision++
	if clone.Revision() == 0 {
		if !terminal {
			return nil, fmt.Errorf("stuntgres: index revision exhausted")
		}
		clone.ObjectRevision = index.Revision()
	}
	return &clone, nil
}
func (e *Engine) evolveStageIndexesLocked(stage *CatalogStage, typeParent ids.ObjectAddress, transformations []columnTransformation) error {
	// Preserve the incumbent map walk. In particular an unrelated staged
	// relation must not allocate an adapter/iterator/yield closure.
	provider := dropProvider{base: stage.Base, owner: stage.owner, path: stage.dependencyPath, stage: stage, ctx: context.Background()}
	for address, descriptor := range stage.Base.Descriptors() {
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
func (provider *dropProvider) indexEvolutionInput(address ids.ObjectAddress, descriptor catalog.Descriptor) (*catalog.Relation, *catalog.Relation, bool, bool) {
	index, ok := descriptor.(*catalog.Relation)
	if !ok || index.SQLIndex == nil || provider.removed(address) {
		return nil, nil, false, false
	}
	candidate := provider.replacement(address)
	staged := candidate != nil
	if staged {
		replacement, ok := candidate.(*catalog.Relation)
		if !ok || replacement.SQLIndex == nil {
			return nil, nil, false, false
		}
		index = replacement
	}
	var parent catalog.Descriptor
	if provider.removed(index.SQLIndex.Parent) {
		parent = nil
	} else if replacement := provider.replacement(index.SQLIndex.Parent); replacement != nil {
		parent = replacement
	} else {
		return nil, nil, false, false
	}
	relation, _ := parent.(*catalog.Relation)
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

func (provider dropProvider) evolveIndex(address ids.ObjectAddress, index, parent *catalog.Relation, staged bool, typeParent ids.ObjectAddress, transformations []columnTransformation) error {
	oldParent := provider.base.MustRelation(index.SQLIndex.Parent)
	prior, chained := provider.indexLayout(address)
	chained = chained && prior.target == index
	if chained {
		oldParent = prior.targetParent
	}
	var dependentTypes []columnTransformation
	// Layout passes evolve the current candidate without lifecycle repair.
	// Actual TYPE dependencies retain their captured reconstruction semantics.
	if index.SQLIndex.Parent == typeParent && (!staged || uniqueIndexTypeAffected(index.SQLIndex, transformations)) {
		dependentTypes = transformations
	}
	next, err := evolveIndexRevision(index, oldParent, parent, dependentTypes, provider.terminalCleanup())
	if err != nil {
		return err
	}
	if next == nil {
		if err := provider.removeObjectLocked(provider.owner.catalog.Domain.Ref(provider.base.Epoch(), address), false); err != nil {
			return err
		}
	} else if next != index {
		if err := provider.replace(next); err != nil {
			return err
		}
	}
	if next != nil && next.SQLIndex.Ready && next.SQLIndex.Live && len(dependentTypes) == 0 && indexLayoutInputsUnchanged(index, oldParent, parent) {
		source, ok := provider.base.Relation(address)
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
func captureIndexStores(relations map[ids.ObjectAddress]*storage.Relation, snapshot *catalog.State) map[ids.ObjectAddress]*storage.Relation {
	var captured map[ids.ObjectAddress]*storage.Relation
	for _, d := range snapshot.Descriptors() {
		index, ok := d.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		if parent, ok := snapshot.Relation(index.SQLIndex.Parent); ok && parent.Partition != nil {
			continue
		}
		if captured == nil {
			captured = make(map[ids.ObjectAddress]*storage.Relation)
		}
		a := index.SQLIndex.Parent
		if captured[a] != nil {
			continue
		}
		if store := relations[a]; store != nil {
			copy := *store
			copy.IndexSource = store
			captured[a] = &copy
		}
	}
	return captured
}
func indexStoresUnchanged(relations, captured map[ids.ObjectAddress]*storage.Relation, current, next *catalog.State, prepared, previous map[ids.ObjectAddress]*storage.IndexRoot) bool {
	if len(prepared) == 0 && len(captured) == 0 {
		return true
	}
	for address, d := range next.Descriptors() {
		index, ok := d.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || prepared != nil && prepared[address] == previous[address] {
			continue
		}
		a := index.SQLIndex.Parent
		if parent, ok := next.Relation(a); ok && parent.Partition != nil {
			continue
		}
		live, old := relations[a], captured[a]
		if current.Get(a) == nil {
			if live != nil || old != nil {
				return false
			}
			continue
		}
		if live == nil || old == nil || live.Generation != old.Generation || live.Ends != old.Ends || len(live.Versions) != len(old.Versions) {
			return false
		}
	}
	return true
}

// Constructors poll cancellation while doing index work; the publication owner
// performs the final veto. An empty/unchanged set must not add an Err callback
// between the owner's established successful observation and installation.
func (e *Engine) prepareIndexCatalog(evaluation *exec.Context, current, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]indexRootCandidate) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	return e.prepareIndexPublication(evaluation, current, next, relations, roots, rebuildParents, rebuildIndexes, nil, true, output, auth, runtimeOwners, candidates)
}

func (e *Engine) prepareIndexPublication(evaluation *exec.Context, current, next *catalog.State, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, publications []rowPublication, materialize bool, output sqltype.OutputEnvironment, auth *AuthorizationView, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]indexRootCandidate) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	ctx := evaluation.Context
	var result map[ids.ObjectAddress]*storage.IndexRoot
	view := e.catalog.read(next)
	evaluation.Catalog, evaluation.Output = view, output
	if auth == nil {
		// Trusted replacement has catalog identity but no SQL principal.
		auth = &AuthorizationView{View: access.View{Catalog: view}}
	} else {
		disclosure := *auth
		disclosure.Catalog = view
		disclosure.Recapture(disclosure.Catalog)
		auth = &disclosure
	}
	if auth.engine != nil {
		local := *evaluation
		local.RoutineAuthority = auth
		evaluation = &local
	}
	for a, descriptor := range next.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent := next.MustRelation(index.SQLIndex.Parent)
		owner, runtime := runtimeOwners[parent.Address()]
		indexAuth := auth
		if runtime && auth.engine != nil {
			if _, err := auth.Roles.Descriptor(owner); err != nil {
				return nil, err
			}
			disclosure := *auth
			disclosure.State.CurrentUser = owner
			indexAuth = &disclosure
		}
		if parent.Partition != nil {
			if current.Get(a) != index {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if err := validateIndexDefinition(view, index); err != nil {
				return nil, err
			}
			if roots[a] != nil {
				return nil, fmt.Errorf("stuntgres: virtual index has physical storage")
			}
			continue
		}
		intent := rebuildParents[index.SQLIndex.Parent] || rebuildIndexes[a] != nil
		if !index.SQLIndex.Ready && rebuildIndexes[a] == nil {
			if current.Get(a) != index {
				if err := validateIndexDefinition(view, index); err != nil {
					return nil, err
				}
			}
			continue
		}
		if materialize && result == nil {
			result = make(map[ids.ObjectAddress]*storage.IndexRoot, len(roots))
		}
		root := roots[a]
		candidate, produced := candidates[a]
		if produced {
			if candidate.root == nil || !candidate.matchesIndex(index, relations[parent.Address()]) {
				return nil, catalog.Conflict{}
			}
			root = candidate.root
		}
		old, _ := current.Relation(a)
		if roots != nil && root == nil && old != nil && old.SQLIndex.Ready && !intent {
			return nil, fmt.Errorf("stuntgres: ready index root is missing")
		}
		rebuild := !produced && (root == nil || old == nil || intent || !sameIndexKeySemantics(old, index))
		// Generic parent shape changes cannot heal invalid indexes. Supplied
		// phase roots and ready-invalid maintenance still use the same core.
		if old != nil && !index.SQLIndex.Valid && rebuildIndexes[a] == nil && !runtime {
			if root == nil {
				return nil, fmt.Errorf("stuntgres: ready index root is missing")
			}
			if old != index {
				if err := validateIndexDefinition(view, index); err != nil {
					return nil, err
				}
			}
			rebuild = false
		}
		if index.SQLIndex.HasRuntimeDomainConversion() && indexAuth.engine != nil {
			needsEvaluation := rebuild
			for _, publication := range publications {
				if publication.address != parent.Address() {
					continue
				}
				for _, row := range publication.writes {
					if !rowMaintainsIndex(row, index) || !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), nil, publication.deletion) {
						continue
					}
					if output, ok := row.IndexOutputs[a]; !ok || !indexOutputMatches(output, index) {
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
		var entries []storage.IndexEntry
		if root != nil {
			entries = root.Entries
		}
		if index.SQLIndex.Method == catalog.HashMethod {
			var err error
			if rebuild {
				if err := validateIndexDefinition(e.catalog.read(next), index); err != nil {
					return nil, err
				}
				root, err = buildHashIndexRoot(evaluation, parent, index.SQLIndex, relations[parent.Address()], materialize || index.SQLIndex.IsExclusion(), nil)
				if err != nil {
					return nil, err
				}
			}
			for i := range publications {
				if publications[i].address != parent.Address() {
					continue
				}
				reconstruct := !produced && (rebuildParents[parent.Address()] || old == nil) && index.SQLIndex.Valid
				root, err = mergeHashIndexRoot(evaluation, parent, index, root, &publications[i], reconstruct, materialize || index.SQLIndex.IsExclusion())
				if err != nil {
					return nil, err
				}
				break
			}
			if index.SQLIndex.IsExclusion() {
				namespace := next.MustNamespace(parent.Namespace())
				if err := validateExclusionRoot(ctx, parent, index, root, namespace.Name(), output, indexAuth, rebuild && !runtime); err != nil {
					return nil, err
				}
			}
			if materialize {
				result[a] = root
			}
			continue
		}
		if rebuild {
			if err := validateIndexDefinition(view, index); err != nil {
				return nil, err
			}
			// Required ordering/uniqueness scratch is not an installation
			// root. PREPARE returns neither a root wrapper nor a root map.
			var err error
			entries, err = buildIndexEntries(evaluation, parent, index.SQLIndex, relations[parent.Address()], nil)
			if err != nil {
				return nil, err
			}
			root = nil
			if index.SQLIndex.Unique || index.SQLIndex.IsExclusion() {
				// Namespace metadata belongs to integrity diagnostics, not ordinary
				// key construction. Replacement also accepts reduced validated
				// parent/index catalogues for ordinary nonintegrity roots.
				namespace := next.MustNamespace(parent.Namespace())
				ordering, err := bindIndexKeys(parent, index.SQLIndex)
				if err != nil {
					return nil, err
				}
				if err := validateUniqueEntries(ctx, parent, index, entries, namespace.Name(), output, indexAuth, !runtime, ordering); err != nil {
					return nil, err
				}
			}
		}
		for i := range publications {
			if publications[i].address != parent.Address() {
				continue
			}
			schema := ""
			if index.SQLIndex.Unique || index.SQLIndex.IsExclusion() {
				schema = next.MustNamespace(parent.Namespace()).Name()
			}
			reconstruct := !produced && (rebuildParents[parent.Address()] || old == nil) && index.SQLIndex.Valid
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
				root = newLogicalIndexRoot(index.SQLIndex, entries)
			}
			result[a] = root
		}
	}
	return result, nil
}

// A backing-constraint rename changes a wrapper, not key/provider semantics.
// Keep roots only when method, key/null-policy and predicate identities are unchanged.
func sameIndexKeySemantics(a, b *catalog.Relation) bool {
	if a == nil || b == nil || a.Address() != b.Address() {
		return false
	}
	if a == b {
		return true
	}
	if !sameIndexDefinitionSemantics(a.SQLIndex, b.SQLIndex) || len(a.Columns) != len(b.Columns) {
		return false
	}
	for i, column := range a.Columns {
		if column.Usage != b.Columns[i].Usage {
			return false
		}
	}
	return true
}

func sameIndexDefinitionSemantics(a, b *catalog.IndexDefinition) bool {
	return a != nil && b != nil && a.Method == b.Method && a.Parent == b.Parent && slices.Equal(a.Keys, b.Keys) && slices.Equal(a.Include, b.Include) && slices.Equal(a.Exclusion, b.Exclusion) && a.Unique == b.Unique && a.NullsNotDistinct == b.NullsNotDistinct && a.Predicate == b.Predicate
}

// Only changed roots require a storage fence. Unchanged siblings may have
// committed while preparation ran; retain their newest immutable roots.
func preserveUnchangedIndexRoots(prepared, previous, live map[ids.ObjectAddress]*storage.IndexRoot) {
	for address, root := range prepared {
		if root == previous[address] {
			prepared[address] = live[address]
		}
	}
}

// Explicit statements must discover derived-key errors before installing private
// rows, not defer them to COMMIT. Admission owns these rows; the captured catalog
// and execution trees are immutable. Finish retains its final publication veto.
func (s *StatementScope) validatePrivateIndexExpressions(changes *session.Journal, deletes map[ids.ObjectAddress]*visibility.PrivateDeletes) error {
	if len(s.stage.rows) == 0 {
		return nil
	}
	e := s.engine()
	e.mu.Lock()
	if s.txRef.Epoch() != e.catalog.state.Epoch() {
		e.mu.Unlock()
		return ids.ErrCatalogReplaced
	}
	view := e.privateCatalogLocked(changes)
	input := s.input
	var programmeAuth AuthorizationView
	if len(s.domains.preparedChecks) != 0 || len(s.routinePrograms) != 0 || len(s.indexBindings) != 0 {
		var err error
		programmeAuth, err = s.writeAuthorizationLocked(e)
		if err != nil {
			e.mu.Unlock()
			return err
		}
		programmeAuth.Recapture(view)
	}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live || !index.SQLIndex.HasRuntimeDomainConversion() || len(s.stage.rows[index.SQLIndex.Parent]) == 0 {
			continue
		}
		auth := programmeAuth
		var err error
		if auth.engine == nil {
			auth, err = s.writeAuthorizationLocked(e)
		}
		if err == nil {
			auth.Catalog = view
			auth.Recapture(view)
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
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Input: input, Output: s.output, Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	for _, descriptor := range view.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || !index.SQLIndex.Ready || !index.SQLIndex.Live {
			continue
		}
		rows := s.stage.rows[index.SQLIndex.Parent]
		if len(rows) == 0 {
			continue
		}
		if index.SQLIndex.Predicate == nil {
			derived := false
			for _, key := range index.SQLIndex.Keys {
				derived = derived || key.Derived != nil
			}
			if !derived {
				continue
			}
		}
		pending := index.SQLIndex.HasRuntimeDomainConversion()
		var ordering indexOrdering
		var hash storage.HashBinding
		var preparation indexExecutionPreparation
		parent, ok := view.Relation(index.SQLIndex.Parent)
		if !ok {
			return ids.ErrCatalogReplaced
		}
		if pending {
			var err error
			if index.SQLIndex.Method == catalog.HashMethod {
				hash, err = bindHashIndex(parent, index.SQLIndex)
			} else {
				ordering, err = bindIndexKeys(parent, index.SQLIndex)
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
			if !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), nil, deletes[index.SQLIndex.Parent]) {
				continue
			}
			if output, ok := row.IndexOutputs[index.Address()]; ok && indexOutputMatches(output, index) {
				// The SQL producer already materialized this exact version's
				// membership/key under the captured index semantics.
				continue
			}
			if pending {
				output, err := materializeIndexOutput(&evaluation, &preparation, parent, index, &ordering, &hash, row.Values)
				if err != nil {
					return err
				}
				outputs := maps.Clone(row.IndexOutputs)
				if outputs == nil {
					outputs = make(map[ids.ObjectAddress]storage.IndexOutput)
				}
				outputs[index.Address()] = output
				if !copied {
					rows = slices.Clone(rows)
					copied = true
				}
				rows[i].IndexOutputs = outputs
				continue
			}
			matches, err := preparation.predicateMatches(&evaluation, parent, index.SQLIndex, row.Values, nil, nil)
			if err != nil {
				return err
			}
			if !matches {
				continue
			}
			if err := preparation.prepareOrdinaryKeys(&evaluation, parent, index.SQLIndex); err != nil {
				return err
			}
			evaluation.Row = row.Values
			for keyIndex, key := range index.SQLIndex.Keys {
				if key.Derived == nil {
					continue
				}
				if _, err := preparation.ordinary[keyIndex].Eval(&evaluation); err != nil {
					return err
				}
			}
		}
		if copied {
			s.stage.rows[index.SQLIndex.Parent] = rows
		}
	}
	return nil
}
