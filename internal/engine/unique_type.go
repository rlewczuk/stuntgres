package engine

import (
	"context"
	"fmt"
	"slices"
	"stuntgres/internal/sqltype"
)

func uniqueTypeAffected(c uniqueConstraint, transformations []columnTransformation) bool {
	for _, change := range transformations {
		if slices.Contains(c.Attributes, change.attribute) {
			return true
		}
	}
	return false
}

// A TYPE action rebuilds each affected constraint/backing relation as one
// removal-and-create candidate, retaining the parent and stable attribute keys.
func (e *Engine) rebuildAlterUniquesLocked(stage *CatalogStage, parent *relationDescriptor, transformations []columnTransformation) (*relationDescriptor, error) {
	if len(parent.uniqueConstraints) == 0 || len(transformations) == 0 {
		return parent, nil
	}
	var next *relationDescriptor
	for i, old := range parent.uniqueConstraints {
		if !uniqueTypeAffected(old, transformations) {
			continue
		}
		if next == nil {
			clone := *parent
			clone.uniqueConstraints = slices.Clone(parent.uniqueConstraints)
			next = &clone
		}
		c := &next.uniqueConstraints[i]
		index, ok := stage.base.state.descriptors[old.Index].(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			return nil, fmt.Errorf("stuntgres: invalid TYPE unique backing index")
		}
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
		definition.constraint = c
		clone.sqlIndex = &definition
		clone.columns = slices.Clone(index.columns)
		for j, key := range definition.keys {
			slot := slices.Index(parent.attributes, key.attribute)
			clone.columns[j].Usage = parent.columns[slot].Usage
			class, ok := sqltype.DefaultIndexClass(clone.columns[j].Usage.Type)
			if !ok {
				return nil, indexDefaultClassError(clone.columns[j].Usage.Type.DiagnosticName(), btreeMethod)
			}
			definition.keys[j].class = class
			clone.columns[j].OriginTable = uint32(id)
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

// TYPE reconstruction follows actual key, INCLUDE and predicate dependencies.
func uniqueIndexTypeAffected(definition *indexDefinition, transformations []columnTransformation) bool {
	for _, change := range transformations {
		if slices.Contains(definition.include, change.attribute) {
			return true
		}
		for _, key := range definition.keys {
			if key.derived == nil {
				if key.attribute == change.attribute {
					return true
				}
				continue
			}
			for _, dependency := range key.derived.dependencies {
				if dependency.Address.ClassID == definition.parent.ClassID && dependency.Address.ObjectID == definition.parent.ObjectID && dependency.Address.SubID == change.attribute {
					return true
				}
			}
		}
		if predicate := definition.predicate; predicate != nil {
			for _, dependency := range predicate.dependencies {
				if dependency.Address.ClassID == definition.parent.ClassID && dependency.Address.ObjectID == definition.parent.ObjectID && dependency.Address.SubID == change.attribute {
					return true
				}
			}
		}
	}
	return false
}

// TYPE must fail at Execute, including private writes. Merge overlay successors
// before building a real unique root; this is not a commit-only integrity fence.
func validateAlterUniques(ctx context.Context, auth AuthorizationView, candidate CatalogRead, parent *relationDescriptor, store *relationStorage, writes []rowVersion, deletes *privateDeletionRoot, schema string, output sqltype.OutputEnvironment, transformations []columnTransformation) (map[string]*logicalIndexRoot, error) {
	indexes := uniqueIndexes(candidate, parent.address)
	affected := false
	for _, index := range indexes {
		if uniqueIndexTypeAffected(index.sqlIndex, transformations) {
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
		if !uniqueIndexTypeAffected(index.sqlIndex, transformations) {
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
func prepareAlterUniqueRoots(ctx context.Context, changes *catalogChanges, parent *relationDescriptor, baseline *relationStorage, validated map[string]*logicalIndexRoot) error {
	for address, descriptor := range changes.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || !index.sqlIndex.unique || index.sqlIndex.parent != parent.address || !index.sqlIndex.ready || !index.sqlIndex.live {
			continue
		}
		if !index.sqlIndex.valid {
			// Generic parent evolution cannot rebuild a failed ready index.
			continue
		}
		if original := changes.originals[address]; original == descriptor {
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
	return nil
}
