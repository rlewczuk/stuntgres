package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Capture accompanies one genuine DDL build. It includes evaluated exclusions
// and NULL hash keys that intentionally have no physical index entry.
type indexBuildCapture struct {
	index   *catalog.Relation
	outputs []indexRowOutput
}

type alterIndexBuild struct {
	root    *logicalIndexRoot
	index   *catalog.Relation
	store   *relationStorage
	outputs []indexRowOutput
}

// A TYPE action rebuilds each affected constraint/backing relation as one
// removal-and-create candidate, retaining the parent and stable attribute keys.
func (e *Engine) rebuildAlterUniquesLocked(stage *CatalogStage, parent *catalog.Relation, transformations []columnTransformation) (*catalog.Relation, error) {
	if len(transformations) == 0 {
		return parent, nil
	}
	var next *catalog.Relation
	for i, old := range parent.Constraints.Unique {
		index, ok := stage.Base.Relation(old.Index)
		if !ok {
			// A TYPE-reminted standalone index was already rebuilt before its
			// USING INDEX attachment. Do not allocate another backing identity.
			if current, exists := stage.Descriptors[old.Index].(*catalog.Relation); exists && current.SQLIndex != nil {
				continue
			}
			return nil, fmt.Errorf("stuntgres: invalid TYPE unique backing index")
		}
		if index.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE unique backing index")
		}
		if current, exists := stage.Descriptors[old.Index].(*catalog.Relation); exists && current.SQLIndex != nil {
			index = current
		}
		if !uniqueIndexTypeAffected(index.SQLIndex, transformations) {
			continue
		}
		if next == nil {
			clone := *parent
			clone.Constraints.Unique = slices.Clone(parent.Constraints.Unique)
			next = &clone
		}
		c := &next.Constraints.Unique[i]
		ref := e.catalog.Domain.Ref(stage.Base.Epoch(), old.Index)
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
		clone.ObjectAddress = c.Index
		clone.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		definition := *index.SQLIndex
		definition.Keys = slices.Clone(index.SQLIndex.Keys)
		definition.Constraint = uniqueBacking(c)
		clone.SQLIndex = &definition
		clone.Columns = slices.Clone(index.Columns)
		for j, key := range definition.Keys {
			attribute, expression, err := indexCatalogKey(parent, key)
			if err != nil || expression != nil {
				return nil, fmt.Errorf("stuntgres: invalid TYPE unique key")
			}
			slot := slices.Index(parent.Attributes, attribute)
			clone.Columns[j].Name = parent.Columns[slot].Name
			clone.Columns[j].Usage = parent.Columns[slot].Usage
			class, ok := sqltype.DefaultIndexClass(clone.Columns[j].Usage.Type)
			if !ok {
				return nil, indexDefaultClassError(clone.Columns[j].Usage.Type.DiagnosticName(), catalog.BtreeMethod)
			}
			definition.Keys[j].Class = class
			clone.Columns[j].OriginTable = uint32(id)
		}
		for j, attribute := range definition.Include {
			slot := slices.Index(parent.Attributes, attribute)
			column := &clone.Columns[len(definition.Keys)+j]
			column.Name, column.Usage, column.OriginTable = parent.Columns[slot].Name, parent.Columns[slot].Usage, uint32(id)
		}
		for j, column := range clone.Columns {
			if column.Name != index.Columns[j].Name {
				clone.Index = make(map[string]int, len(clone.Columns))
				for slot, renamed := range clone.Columns {
					clone.Index[renamed.Name] = slot
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
func uniqueIndexTypeAffected(definition *catalog.IndexDefinition, transformations []columnTransformation) bool {
	for _, change := range transformations {
		if indexAttributeAffected(definition, change.attribute) {
			return true
		}
	}
	return false
}

func uniqueIndexValuesAffected(definition *catalog.IndexDefinition, transformations []columnTransformation, generations []int32) bool {
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

func indexAttributeAffected(definition *catalog.IndexDefinition, attribute int32) bool {
	if slices.Contains(definition.Include, attribute) {
		return true
	}
	dependent := func(ref ids.ObjectRef) bool {
		return ref.Address.ClassID == definition.Parent.ClassID && ref.Address.ObjectID == definition.Parent.ObjectID && ref.Address.SubID == attribute
	}
	for _, key := range definition.Keys {
		if key.Derived == nil {
			if key.Attribute == attribute {
				return true
			}
		} else {
			for _, ref := range key.Derived.Dependencies {
				if dependent(ref) {
					return true
				}
			}
		}
	}
	if predicate := definition.Predicate; predicate != nil {
		for _, ref := range predicate.Dependencies {
			if dependent(ref) {
				return true
			}
		}
	}
	return false
}

// TYPE must fail at Execute, including private writes. Merge overlay successors
// before building a real unique root; this is not a commit-only integrity fence.
func validateAlterIndexBuilds(evaluation *EvalContext, auth AuthorizationView, candidate catalog.Read, parent *catalog.Relation, store *relationStorage, writes []rowVersion, deletes *privateDeletionRoot, schema string, output sqltype.OutputEnvironment, transformations []columnTransformation, generations []int32, added []ids.ObjectAddress, physicalRewrite bool) (map[string]*alterIndexBuild, error) {
	ctx := evaluation.Context
	if parent.Partition != nil {
		return nil, nil
	}
	var indexes []*catalog.Relation
	if physicalRewrite || len(transformations) != 0 || len(generations) != 0 {
		for _, descriptor := range candidate.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() || !index.SQLIndex.Ready || !index.SQLIndex.Live || !index.SQLIndex.Valid {
				continue
			}
			if !slices.Contains(added, index.Address()) && (physicalRewrite || uniqueIndexValuesAffected(index.SQLIndex, transformations, generations)) {
				indexes = append(indexes, index)
			}
		}
	}
	for _, address := range added {
		index, ok := candidate.Relation(address)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() {
			return nil, ids.ErrCatalogReplaced
		}
		indexes = append(indexes, index)
	}
	if len(indexes) == 0 {
		return nil, nil
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
		if index.SQLIndex.HasRuntimeDomainConversion() && !named {
			var err error
			evaluation.Input.TypeDiagnosticName, err = auth.engine.captureTypeDiagnosticName(auth)
			if err != nil {
				return nil, err
			}
			named = true
		}
		if index.SQLIndex.HasRuntimeDomainConversion() || len(writes) != 0 {
			capture = &indexBuildCapture{index: index, outputs: make([]indexRowOutput, len(validationStore.Versions))}
		}
		root, err := buildIndexRoot(evaluation, parent, index.SQLIndex, validationStore, capture)
		if err != nil {
			return nil, err
		}
		if index.SQLIndex.Valid || index.SQLIndex.Constraint != nil {
			if err := validateUniqueRoot(ctx, parent, index, root, schema, output, &auth, true); err != nil {
				return nil, err
			}
		}
		build := &alterIndexBuild{index: index, root: root, store: validationStore}
		if capture != nil {
			build.outputs = capture.outputs
		}
		builds[index.Name()] = build
	}
	return builds, nil
}

// A genuine DDL build is reusable even with an own overlay: derive the baseline
// from produced entries, and carry exact private pins rather than invoking SQL.
func prepareAlterIndexRoots(evaluation *EvalContext, changes *catalogChanges, source catalog.Read, parent *catalog.Relation, baseline *relationStorage, validated map[string]*alterIndexBuild, metadataOnly bool) error {
	if parent.Partition != nil {
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
		descriptor, changed := changes.descriptors[build.index.Address()]
		if !changed {
			descriptor = evaluation.catalog.Get(build.index.Address())
		}
		index, ok := descriptor.(*catalog.Relation)
		if !ok || !sameIndexKeySemantics(build.index, index) {
			return ids.ErrCatalogReplaced
		}
		root, err := build.baselineRoot(evaluation.Context, index, baseline, positions)
		if err != nil {
			return err
		}
		retainIndexBuild(changes, index, baseline, root)
	}
	for address, descriptor := range changes.descriptors {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil || index.SQLIndex.Parent != parent.Address() || !index.SQLIndex.Ready || !index.SQLIndex.Live || !index.SQLIndex.Valid {
			continue
		}
		if build := validated[index.Name()]; build != nil && build.index.Address() == address && sameIndexKeySemantics(build.index, index) {
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
		root, err := buildIndexRoot(evaluation, parent, index.SQLIndex, baseline, nil)
		if err != nil {
			return err
		}
		retainIndexBuild(changes, index, baseline, root)
	}
	return evaluation.Context.Err()
}
