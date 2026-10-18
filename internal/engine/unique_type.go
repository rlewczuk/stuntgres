package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Capture accompanies one genuine DDL build. It includes evaluated exclusions
// and NULL hash keys that intentionally have no physical index entry.
type indexBuildCapture struct {
	index   *relationDescriptor
	outputs []indexRowOutput
}

type alterIndexBuild struct {
	root    *logicalIndexRoot
	index   *relationDescriptor
	store   *relationStorage
	outputs []indexRowOutput
}

// A TYPE action rebuilds each affected constraint/backing relation as one
// removal-and-create candidate, retaining the parent and stable attribute keys.
func (e *Engine) rebuildAlterUniquesLocked(stage *CatalogStage, parent *relationDescriptor, transformations []columnTransformation) (*relationDescriptor, error) {
	if len(transformations) == 0 {
		return parent, nil
	}
	var next *relationDescriptor
	for i, old := range parent.uniqueConstraints {
		index, ok := stage.base.Relation(old.Index)
		if !ok {
			// A TYPE-reminted standalone index was already rebuilt before its
			// USING INDEX attachment. Do not allocate another backing identity.
			if current, exists := stage.descriptors[old.Index].(*relationDescriptor); exists && current.sqlIndex != nil {
				continue
			}
			return nil, fmt.Errorf("stuntgres: invalid TYPE unique backing index")
		}
		if index.sqlIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE unique backing index")
		}
		if current, exists := stage.descriptors[old.Index].(*relationDescriptor); exists && current.sqlIndex != nil {
			index = current
		}
		if !uniqueIndexTypeAffected(index.sqlIndex, transformations) {
			continue
		}
		if next == nil {
			clone := *parent
			clone.uniqueConstraints = slices.Clone(parent.uniqueConstraints)
			next = &clone
		}
		c := &next.uniqueConstraints[i]
		ref := e.catalog.domain.Ref(stage.base.Epoch(), old.Index)
		if err := stage.removeObjectLocked(ref, false); err != nil {
			return nil, err
		}
		var err error
		c.OID, err = e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		id, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		c.Index = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
		clone := *index
		clone.address = c.Index
		clone.revision, err = e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		definition := *index.sqlIndex
		definition.keys = slices.Clone(index.sqlIndex.keys)
		definition.constraint = uniqueBacking(c)
		clone.sqlIndex = &definition
		clone.columns = slices.Clone(index.columns)
		for j, key := range definition.keys {
			attribute, expression, err := indexCatalogKey(parent, key)
			if err != nil || expression != nil {
				return nil, fmt.Errorf("stuntgres: invalid TYPE unique key")
			}
			slot := slices.Index(parent.attributes, attribute)
			clone.columns[j].Name = parent.columns[slot].Name
			clone.columns[j].Usage = parent.columns[slot].Usage
			class, ok := sqltype.DefaultIndexClass(clone.columns[j].Usage.Type)
			if !ok {
				return nil, indexDefaultClassError(clone.columns[j].Usage.Type.DiagnosticName(), btreeMethod)
			}
			definition.keys[j].class = class
			clone.columns[j].OriginTable = uint32(id)
		}
		for j, attribute := range definition.include {
			slot := slices.Index(parent.attributes, attribute)
			column := &clone.columns[len(definition.keys)+j]
			column.Name, column.Usage, column.OriginTable = parent.columns[slot].Name, parent.columns[slot].Usage, uint32(id)
		}
		for j, column := range clone.columns {
			if column.Name != index.columns[j].Name {
				clone.index = make(map[string]int, len(clone.columns))
				for slot, renamed := range clone.columns {
					clone.index[renamed.Name] = slot
				}
				break
			}
		}
		if err := stage.Create(&clone); err != nil {
			return nil, err
		}
	}
	if next == nil {
		return e.rebuildAlterNotNullsLocked(stage, parent, transformations)
	}
	if err := stage.Replace(next); err != nil {
		return nil, err
	}
	return e.rebuildAlterNotNullsLocked(stage, next, transformations)
}

// TYPE reconstruction and changed-value validation share stable dependency keys.
func uniqueIndexTypeAffected(definition *indexDefinition, transformations []columnTransformation) bool {
	for _, change := range transformations {
		if indexAttributeAffected(definition, change.attribute) {
			return true
		}
	}
	return false
}

func uniqueIndexValuesAffected(definition *indexDefinition, transformations []columnTransformation, generations []int32) bool {
	if uniqueIndexTypeAffected(definition, transformations) {
		return true
	}
	for _, attribute := range generations {
		if indexAttributeAffected(definition, attribute) {
			return true
		}
	}
	return false
}

func indexAttributeAffected(definition *indexDefinition, attribute int32) bool {
	if slices.Contains(definition.include, attribute) {
		return true
	}
	dependent := func(ref ids.ObjectRef) bool {
		return ref.Address.ClassID == definition.parent.ClassID && ref.Address.ObjectID == definition.parent.ObjectID && ref.Address.SubID == attribute
	}
	for _, key := range definition.keys {
		if key.derived == nil {
			if key.attribute == attribute {
				return true
			}
		} else {
			for _, ref := range key.derived.dependencies {
				if dependent(ref) {
					return true
				}
			}
		}
	}
	if predicate := definition.predicate; predicate != nil {
		for _, ref := range predicate.dependencies {
			if dependent(ref) {
				return true
			}
		}
	}
	return false
}

// TYPE must fail at Execute, including private writes. Merge overlay successors
// before building a real unique root; this is not a commit-only integrity fence.
func validateAlterIndexBuilds(evaluation *EvalContext, auth AuthorizationView, candidate CatalogRead, parent *relationDescriptor, store *relationStorage, writes []rowVersion, deletes *privateDeletionRoot, schema string, output sqltype.OutputEnvironment, transformations []columnTransformation, generations []int32, added []ids.ObjectAddress, physicalRewrite bool) (map[string]*alterIndexBuild, error) {
	ctx := evaluation.Context
	if parent.partition != nil {
		return nil, nil
	}
	var indexes []*relationDescriptor
	if physicalRewrite || len(transformations) != 0 || len(generations) != 0 {
		for _, descriptor := range candidate.Descriptors() {
			index, ok := descriptor.(*relationDescriptor)
			if !ok || index.sqlIndex == nil || index.sqlIndex.parent != parent.address || !index.sqlIndex.ready || !index.sqlIndex.live || !index.sqlIndex.valid {
				continue
			}
			if !slices.Contains(added, index.address) && (physicalRewrite || uniqueIndexValuesAffected(index.sqlIndex, transformations, generations)) {
				indexes = append(indexes, index)
			}
		}
	}
	for _, address := range added {
		index, ok := candidate.Relation(address)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != parent.address {
			return nil, ErrCatalogReplaced
		}
		indexes = append(indexes, index)
	}
	if len(indexes) == 0 {
		return nil, nil
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
	builds := make(map[string]*alterIndexBuild, len(indexes))
	validationStore, err := indexBuildVisibleStore(ctx, store, writes, deletes)
	if err != nil {
		return nil, err
	}
	auth.catalog = candidate
	auth.recaptureCatalog(auth.catalog)
	evaluation.catalog, evaluation.Output = candidate, output
	named := false
	for _, index := range indexes {
		var capture *indexBuildCapture
		if index.sqlIndex.hasRuntimeDomainConversion() && !named {
			var err error
			evaluation.Input.TypeDiagnosticName, err = auth.engine.captureTypeDiagnosticName(auth)
			if err != nil {
				return nil, err
			}
			named = true
		}
		if index.sqlIndex.hasRuntimeDomainConversion() || len(writes) != 0 {
			capture = &indexBuildCapture{index: index, outputs: make([]indexRowOutput, len(validationStore.versions))}
		}
		root, err := buildIndexRoot(evaluation, parent, index.sqlIndex, validationStore, capture)
		if err != nil {
			return nil, err
		}
		if index.sqlIndex.valid || index.sqlIndex.constraint != nil {
			if err := validateUniqueRoot(ctx, parent, index, root, schema, output, &auth, true); err != nil {
				return nil, err
			}
		}
		build := &alterIndexBuild{index: index, root: root, store: validationStore}
		if capture != nil {
			build.outputs = capture.outputs
		}
		builds[index.name] = build
	}
	return builds, nil
}

// A genuine DDL build is reusable even with an own overlay: derive the baseline
// from produced entries, and carry exact private pins rather than invoking SQL.
func prepareAlterIndexRoots(evaluation *EvalContext, changes *catalogChanges, source CatalogRead, parent *relationDescriptor, baseline *relationStorage, validated map[string]*alterIndexBuild, metadataOnly bool) error {
	if parent.partition != nil {
		return nil
	}
	var positions map[indexRowIdentity]int
	for _, build := range validated {
		if build.store != baseline {
			var err error
			positions, err = indexBuildPositions(evaluation.Context, baseline)
			if err != nil {
				return err
			}
			break
		}
	}
	for _, build := range validated {
		descriptor, changed := changes.descriptors[build.index.address]
		if !changed {
			descriptor = evaluation.catalog.Get(build.index.address)
		}
		index, ok := descriptor.(*relationDescriptor)
		if !ok || !sameIndexKeySemantics(build.index, index) {
			return ErrCatalogReplaced
		}
		root, err := build.baselineRoot(evaluation.Context, index, baseline, positions)
		if err != nil {
			return err
		}
		retainIndexBuild(changes, index, baseline, root)
	}
	for address, descriptor := range changes.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != parent.address || !index.sqlIndex.ready || !index.sqlIndex.live || !index.sqlIndex.valid {
			continue
		}
		if build := validated[index.name]; build != nil && build.index.address == address && sameIndexKeySemantics(build.index, index) {
			continue
		}
		if candidate, produced := changes.indexCandidates[address]; produced && candidate.matchesIndex(index, baseline) {
			continue
		}
		if original := changes.originals[address]; original == descriptor {
			continue
		}
		if metadataOnly {
			if previous, ok := source.Relation(address); ok && sameIndexKeySemantics(previous, index) {
				continue
			}
		}
		root, err := buildIndexRoot(evaluation, parent, index.sqlIndex, baseline, nil)
		if err != nil {
			return err
		}
		retainIndexBuild(changes, index, baseline, root)
	}
	return evaluation.Context.Err()
}
