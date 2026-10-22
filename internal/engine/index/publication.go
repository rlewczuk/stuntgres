package index

import (
	"fmt"
	"iter"
	"slices"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// Return a detached descriptor when a dependent attribute TYPE or schema/owner
// changes. TYPE reconstructs the whole definition using OLD type defaults,
// matching PostgreSQL's captured pg_get_indexdef rather than original spelling.
func EvolveIndex(view catalog.Read, index, oldParent, parent *catalog.Relation, transformations []ColumnTransformation) (*catalog.Relation, error) {
	return evolveIndexRevision(view, index, oldParent, parent, transformations, false)
}

func evolveIndexRevision(view catalog.Read, index, oldParent, parent *catalog.Relation, transformations []ColumnTransformation, terminal bool) (*catalog.Relation, error) {
	if parent == nil {
		return nil, nil
	}
	if index.SQLIndex.Constraint != nil && !BackingConstraintPresent(parent, index) {
		return nil, nil
	}
	definition := index.SQLIndex
	reconstruct := false
	dependentAttribute := func(attribute int32) bool {
		if !parent.HasAttribute(attribute) {
			return false
		}
		for _, transformation := range transformations {
			if transformation.Attribute == attribute {
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
		node, err := stored.RemapExpression(key.Derived.Definition, remap, slots)
		if err != nil {
			return nil, err
		}
		execution, err := stored.RemapExpression(key.Derived.Execution, remap, slots)
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
			node, err := stored.RemapExpression(predicate.Definition, remap, slots)
			if err != nil {
				return nil, err
			}
			execution, err := stored.RemapExpression(predicate.Execution, remap, slots)
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
			definition.Keys[i] = ReconstructIndexKey(view, key, oldUsage, newUsage, definition.MethodIdentity())
		}
	}
	if err := bindIndexProvider(view, parent, definition); err != nil {
		return nil, err
	}
	changed := definition != index.SQLIndex || index.Namespace() != parent.Namespace() || index.Owner != parent.Owner
	for i, key := range definition.Keys {
		usage, err := KeyUsage(parent, key)
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
		usage, err := KeyUsage(parent, key)
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

// EvolutionReader is what selecting an index for evolution reads: the
// staged replacement and removal of a descriptor. *CatalogStage and
// *dropProvider implement it.
type EvolutionReader interface {
	Removed(ids.ObjectAddress) bool
	Replacement(ids.ObjectAddress) catalog.Descriptor
}

// EvolutionTarget is what evolving an index reads and writes in a drop
// or layout pass. *dropProvider implements it.
type EvolutionTarget interface {
	EvolutionReader
	InputDescriptors() iter.Seq2[ids.ObjectAddress, catalog.Descriptor]
	EvolutionBase() catalog.Read
	IndexLayout(ids.ObjectAddress) (LayoutLineage, bool)
	RecordIndexLayout(ids.ObjectAddress, LayoutLineage)
	TerminalCleanup() bool
	RemoveIndex(ids.ObjectAddress) error
	Replace(catalog.Descriptor) error
}

// EvolutionInput selects an index whose parent a pass replaced or
// removed, and returns the index, its new parent (nil when removed), and
// whether the index itself is staged. The selector is shared and read-only.
func EvolutionInput(provider EvolutionReader, address ids.ObjectAddress, descriptor catalog.Descriptor) (*catalog.Relation, *catalog.Relation, bool, bool) {
	index, ok := descriptor.(*catalog.Relation)
	if !ok || index.SQLIndex == nil || provider.Removed(address) {
		return nil, nil, false, false
	}
	candidate := provider.Replacement(address)
	staged := candidate != nil
	if staged {
		replacement, ok := candidate.(*catalog.Relation)
		if !ok || replacement.SQLIndex == nil {
			return nil, nil, false, false
		}
		index = replacement
	}
	var parent catalog.Descriptor
	if provider.Removed(index.SQLIndex.Parent) {
		parent = nil
	} else if replacement := provider.Replacement(index.SQLIndex.Parent); replacement != nil {
		parent = replacement
	} else {
		return nil, nil, false, false
	}
	relation, _ := parent.(*catalog.Relation)
	return index, relation, staged, true
}

// Prepared and ordinary adapters share the exact evolution/application worker.
func EvolveIndexes(provider EvolutionTarget, typeParent ids.ObjectAddress, transformations []ColumnTransformation) error {
	for address, descriptor := range provider.InputDescriptors() {
		index, parent, staged, affected := EvolutionInput(provider, address, descriptor)
		if !affected {
			continue
		}
		if err := ApplyIndexEvolution(provider, address, index, parent, staged, typeParent, transformations); err != nil {
			return err
		}
	}
	return nil
}

// ApplyIndexEvolution evolves one selected index: it replaces or removes the
// index and records a slot-only layout lineage.
func ApplyIndexEvolution(provider EvolutionTarget, address ids.ObjectAddress, index, parent *catalog.Relation, staged bool, typeParent ids.ObjectAddress, transformations []ColumnTransformation) error {
	oldParent := provider.EvolutionBase().MustRelation(index.SQLIndex.Parent)
	prior, chained := provider.IndexLayout(address)
	chained = chained && prior.Target == index
	if chained {
		oldParent = prior.TargetParent
	}
	var dependentTypes []ColumnTransformation
	// Layout passes evolve the current candidate without lifecycle repair.
	// Actual TYPE dependencies retain their captured reconstruction semantics.
	if index.SQLIndex.Parent == typeParent && (!staged || UniqueIndexTypeAffected(index.SQLIndex, transformations)) {
		dependentTypes = transformations
	}
	next, err := evolveIndexRevision(provider.EvolutionBase(), index, oldParent, parent, dependentTypes, provider.TerminalCleanup())
	if err != nil {
		return err
	}
	if next == nil {
		if err := provider.RemoveIndex(address); err != nil {
			return err
		}
	} else if next != index {
		if err := provider.Replace(next); err != nil {
			return err
		}
	}
	if next != nil && next.SQLIndex.Ready && next.SQLIndex.Live && len(dependentTypes) == 0 && LayoutInputsUnchanged(index, oldParent, parent) {
		source, ok := provider.EvolutionBase().Relation(address)
		if chained {
			source, ok = index, true
		}
		if ok && SameIndexKeySemantics(source, index) {
			if chained && next == prior.Target && parent == prior.TargetParent {
				return nil
			}
			lineage := LayoutLineage{Source: source, Target: next, SourceParent: oldParent, TargetParent: parent}
			if chained {
				lineage.Previous = &prior
			}
			provider.RecordIndexLayout(address, lineage)
		}
	}
	return nil
}

// Captured heads are immutable; the copied scalar generation/end-root tuple
// fences every build against row publication, even when catalog is unchanged.
func CaptureIndexStores(relations map[ids.ObjectAddress]*storage.Relation, snapshot *catalog.State) map[ids.ObjectAddress]*storage.Relation {
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

func StoresUnchanged(relations, captured map[ids.ObjectAddress]*storage.Relation, current, next *catalog.State, prepared, previous map[ids.ObjectAddress]*storage.IndexRoot) bool {
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

// PrepareIndexPublication builds and validates the index roots of next
// against the row publications. view reads next. Duplicate keys are reported
// to disclosure, a view of env recaptured over view; a nil env is trusted
// publication, which has no SQL principal. Runtime owners replace the current
// user of their tables' indexes.
func PrepareIndexPublication(evaluation *exec.Context, current, next *catalog.State, view catalog.Read, relations map[ids.ObjectAddress]*storage.Relation, roots map[ids.ObjectAddress]*storage.IndexRoot, rebuildParents map[ids.ObjectAddress]bool, rebuildIndexes map[ids.ObjectAddress]*catalog.Relation, publications []RowPublication, materialize bool, output sqltype.OutputEnvironment, disclosure access.View, env DisclosureEnv, runtimeOwners map[ids.ObjectAddress]authorization.PrincipalRef, candidates map[ids.ObjectAddress]RootCandidate) (map[ids.ObjectAddress]*storage.IndexRoot, error) {
	ctx := evaluation.Context
	var result map[ids.ObjectAddress]*storage.IndexRoot
	evaluation.Catalog, evaluation.Output = view, output
	if env != nil {
		local := *evaluation
		local.RoutineAuthority = env
		evaluation = &local
	}
	for a, descriptor := range next.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		parent := next.MustRelation(index.SQLIndex.Parent)
		owner, runtime := runtimeOwners[parent.Address()]
		indexView := disclosure
		if runtime && env != nil {
			if _, err := disclosure.Roles.Descriptor(owner); err != nil {
				return nil, err
			}
			indexView.State.CurrentUser = owner
		}
		if parent.Partition != nil {
			if current.Get(a) != index {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			if err := ValidateIndexDefinition(view, index); err != nil {
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
				if err := ValidateIndexDefinition(view, index); err != nil {
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
			if candidate.Root == nil || !candidate.MatchesIndex(index, relations[parent.Address()]) {
				return nil, catalog.Conflict{}
			}
			root = candidate.Root
		}
		old, _ := current.Relation(a)
		if roots != nil && root == nil && old != nil && old.SQLIndex.Ready && !intent {
			return nil, fmt.Errorf("stuntgres: ready index root is missing")
		}
		rebuild := !produced && (root == nil || old == nil || intent || !SameIndexKeySemantics(old, index))
		// Generic parent shape changes cannot heal invalid indexes. Supplied
		// phase roots and ready-invalid maintenance still use the same core.
		if old != nil && !index.SQLIndex.Valid && rebuildIndexes[a] == nil && !runtime {
			if root == nil {
				return nil, fmt.Errorf("stuntgres: ready index root is missing")
			}
			if old != index {
				if err := ValidateIndexDefinition(view, index); err != nil {
					return nil, err
				}
			}
			rebuild = false
		}
		if index.SQLIndex.HasRuntimeDomainConversion() && env != nil {
			needsEvaluation := rebuild
			for _, publication := range publications {
				if publication.Address != parent.Address() {
					continue
				}
				for _, row := range publication.Writes {
					if !RowMaintainsIndex(row, index) || !visibility.LiveVersion(row, ids.CommitSeq(^uint64(0)), nil, publication.Deletion) {
						continue
					}
					if output, ok := row.IndexOutputs[a]; !ok || !OutputMatches(output, index) {
						needsEvaluation = true
						break
					}
				}
				break
			}
			if needsEvaluation {
				var err error
				evaluation.Input.TypeDiagnosticName, err = env.TypeDiagnosticNames(indexView)
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
				if err := ValidateIndexDefinition(view, index); err != nil {
					return nil, err
				}
				root, err = BuildHashIndexRoot(evaluation, parent, index.SQLIndex, relations[parent.Address()], materialize || index.SQLIndex.IsExclusion(), nil)
				if err != nil {
					return nil, err
				}
			}
			for i := range publications {
				if publications[i].Address != parent.Address() {
					continue
				}
				reconstruct := !produced && (rebuildParents[parent.Address()] || old == nil) && index.SQLIndex.Valid
				root, err = MergeHashIndexRoot(evaluation, parent, index, root, &publications[i], reconstruct, materialize || index.SQLIndex.IsExclusion())
				if err != nil {
					return nil, err
				}
				break
			}
			if index.SQLIndex.IsExclusion() {
				namespace := next.MustNamespace(parent.Namespace())
				if err := ValidateExclusionRoot(ctx, parent, index, root, namespace.Name(), output, &indexView, env, rebuild && !runtime); err != nil {
					return nil, err
				}
			}
			if materialize {
				result[a] = root
			}
			continue
		}
		if rebuild {
			if err := ValidateIndexDefinition(view, index); err != nil {
				return nil, err
			}
			// Required ordering/uniqueness scratch is not an installation
			// root. PREPARE returns neither a root wrapper nor a root map.
			var err error
			entries, err = BuildIndexEntries(evaluation, parent, index.SQLIndex, relations[parent.Address()], nil)
			if err != nil {
				return nil, err
			}
			root = nil
			if index.SQLIndex.Unique || index.SQLIndex.IsExclusion() {
				// Namespace metadata belongs to integrity diagnostics, not ordinary
				// key construction. Replacement also accepts reduced validated
				// parent/index catalogues for ordinary nonintegrity roots.
				namespace := next.MustNamespace(parent.Namespace())
				ordering, err := BindIndexKeys(view, parent, index.SQLIndex)
				if err != nil {
					return nil, err
				}
				if err := ValidateUniqueEntries(ctx, parent, index, entries, namespace.Name(), output, &indexView, env, !runtime, ordering); err != nil {
					return nil, err
				}
			}
		}
		for i := range publications {
			if publications[i].Address != parent.Address() {
				continue
			}
			schema := ""
			if index.SQLIndex.Unique || index.SQLIndex.IsExclusion() {
				schema = next.MustNamespace(parent.Namespace()).Name()
			}
			reconstruct := !produced && (rebuildParents[parent.Address()] || old == nil) && index.SQLIndex.Valid
			merged, err := MergeIndexRoot(evaluation, parent, index, entries, &publications[i], reconstruct, materialize, false, schema, output, &indexView, env)
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
				root = NewLogicalIndexRoot(index.SQLIndex, entries)
			}
			result[a] = root
		}
	}
	return result, nil
}

// A backing-constraint rename changes a wrapper, not key/provider semantics.
// Keep roots only when method, key/null-policy and predicate identities are unchanged.
func SameIndexKeySemantics(a, b *catalog.Relation) bool {
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
func PreserveUnchangedIndexRoots(prepared, previous, live map[ids.ObjectAddress]*storage.IndexRoot) {
	for address, root := range prepared {
		if root == previous[address] {
			prepared[address] = live[address]
		}
	}
}

// Prepare immutable replacement heads/end roots outside final locks. Appends
// retain geometric capacity; final installation follows one all-relations veto.
type RowPublication struct {
	Address            ids.ObjectAddress
	Store              *storage.Relation
	Original           *storage.Relation
	OriginalRelation   *catalog.Relation
	Relation           *catalog.Relation
	Root, Next, Writes []storage.RowVersion
	Ends, NextEnds     *storage.DeletionRoot
	Deletion           *visibility.PrivateDeletes
	Generation         uint64
	Slots              []int
	SingleSlot         int
}
