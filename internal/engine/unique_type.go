package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqltype"
)

// A TYPE action rebuilds each affected constraint/backing relation as one
// removal-and-create candidate, retaining the parent and stable attribute keys.
func (e *Engine) rebuildAlterUniquesLocked(stage *CatalogStage, parent *relationDescriptor, transformations []columnTransformation) (*relationDescriptor, error) {
	if len(parent.uniqueConstraints) == 0 || len(transformations) == 0 {
		return parent, nil
	}
	var next *relationDescriptor
	for i, old := range parent.uniqueConstraints {
		index, ok := stage.base.state.descriptors[old.Index].(*relationDescriptor)
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
		ref := ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: old.Index}
		if err := stage.removeObjectLocked(ref, false); err != nil {
			return nil, err
		}
		var err error
		c.OID, err = e.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		id, err := e.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		c.Index = ObjectAddress{ClassID: RelationClassID, ObjectID: id}
		clone := *index
		clone.address = c.Index
		clone.revision, err = e.revisionLocked()
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
		return parent, nil
	}
	// PostgreSQL18 rebuilds the affected primary key's NN identity after the
	// index constraint. Clone only on change: source/savepoint descriptors keep
	// their old identities, and unrelated NN identities remain untouched.
	copiedNotNull := false
	for i, n := range parent.notNullConstraints {
		changed := false
		for _, transformation := range transformations {
			if transformation.attribute != n.Attribute {
				continue
			}
			for _, constraint := range parent.uniqueConstraints {
				if constraint.Primary && slices.Contains(constraint.Attributes, n.Attribute) {
					changed = true
					break
				}
			}
		}
		if !changed {
			continue
		}
		if !copiedNotNull {
			next.notNullConstraints = slices.Clone(parent.notNullConstraints)
			copiedNotNull = true
		}
		id, err := e.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		next.notNullConstraints[i].OID = id
	}
	if err := stage.Replace(next); err != nil {
		return nil, err
	}
	return next, nil
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
	dependent := func(ref ObjectRef) bool {
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
func validateAlterUniques(ctx context.Context, auth AuthorizationView, candidate CatalogRead, parent *relationDescriptor, store *relationStorage, writes []rowVersion, deletes *privateDeletionRoot, schema string, output sqltype.OutputEnvironment, transformations []columnTransformation, generations []int32) (map[string]*logicalIndexRoot, error) {
	indexes := enforcingIndexes(candidate, parent.address)
	affected := false
	for _, index := range indexes {
		if uniqueIndexValuesAffected(index.sqlIndex, transformations, generations) {
			affected = true
			break
		}
	}
	if !affected {
		return nil, nil
	}
	var baselines map[string]*logicalIndexRoot
	if len(writes) == 0 && (deletes == nil || len(deletes.rows) == 0) {
		baselines = make(map[string]*logicalIndexRoot)
	}
	validationStore := store
	if baselines == nil {
		// Overlay-only validation is never installed. Reusable roots instead
		// retain the original physical layout, including committed tombstones.
		replaced := make(map[RowID]bool, len(writes))
		for _, row := range writes {
			replaced[row.row] = true
		}
		var live []rowVersion
		for _, row := range store.versions {
			if !replaced[row.row] && liveVersion(row, CommitSeq(^uint64(0)), store.ends, deletes) {
				live = append(live, row)
			}
		}
		for _, row := range writes {
			if !uniqueRowDeleted(row.row, deletes) {
				live = append(live, row)
			}
		}
		validationStore = &relationStorage{versions: live}
	}
	for _, index := range indexes {
		if !uniqueIndexValuesAffected(index.sqlIndex, transformations, generations) {
			continue
		}
		root, err := buildIndexRoot(ctx, parent, index.sqlIndex, validationStore)
		if err != nil {
			return nil, err
		}
		if err := validateUniqueRoot(ctx, parent, index, root, schema, output, &auth, true); err != nil {
			return nil, err
		}
		if baselines != nil {
			baselines[index.name] = root
		}
	}
	return baselines, nil
}

// Both reminted attachments and evolved standalone indexes use the existing
// private catalog owner. Roots exclude own successors, which DML checks in its
// overlay; validated roots are reusable only when there was no own overlay.
func prepareAlterUniqueRoots(ctx context.Context, changes *catalogChanges, source CatalogRead, parent *relationDescriptor, baseline *relationStorage, validated map[string]*logicalIndexRoot, metadataOnly bool, generations []int32) error {
	for address, descriptor := range changes.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || (!index.sqlIndex.unique && !index.sqlIndex.isExclusion()) || index.sqlIndex.parent != parent.address || !index.sqlIndex.ready || !index.sqlIndex.live {
			continue
		}
		if !index.sqlIndex.valid {
			// Generic parent evolution cannot rebuild a failed ready index.
			continue
		}
		if original := changes.originals[address]; original == descriptor {
			continue
		}
		if metadataOnly {
			if previous, ok := source.state.descriptors[address].(*relationDescriptor); ok && sameIndexKeySemantics(previous, index) {
				continue // Ownership/name/NN metadata alone retains the exact root.
			}
		}
		root := validated[index.name]
		if root == nil {
			var err error
			root, err = buildIndexRoot(ctx, parent, index.sqlIndex, baseline)
			if err != nil {
				return err
			}
		}
		if changes.uniqueRoots == nil {
			changes.uniqueRoots = make(map[ObjectAddress]*logicalIndexRoot)
		}
		changes.uniqueRoots[address] = root
	}
	// Type-preserving generation SET keeps index/constraint identities. Install
	// new baseline roots even when no descriptor evolution was necessary.
	if len(generations) != 0 {
		for address, descriptor := range source.state.descriptors {
			index, ok := descriptor.(*relationDescriptor)
			if !ok || index.sqlIndex == nil || (!index.sqlIndex.unique && !index.sqlIndex.isExclusion()) || index.sqlIndex.parent != parent.address || !index.sqlIndex.ready || !index.sqlIndex.live || !index.sqlIndex.valid || !uniqueIndexValuesAffected(index.sqlIndex, nil, generations) {
				continue
			}
			if _, changed := changes.descriptors[address]; changed {
				continue
			}
			root := validated[index.name]
			if root == nil {
				var err error
				root, err = buildIndexRoot(ctx, parent, index.sqlIndex, baseline)
				if err != nil {
					return err
				}
			}
			if changes.uniqueRoots == nil {
				changes.uniqueRoots = make(map[ObjectAddress]*logicalIndexRoot)
			}
			changes.uniqueRoots[address] = root
		}
	}
	return nil
}
