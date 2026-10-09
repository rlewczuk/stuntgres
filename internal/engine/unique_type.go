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
		definition.unique = c
		clone.sqlIndex = &definition
		clone.columns = slices.Clone(index.columns)
		for j, key := range definition.keys {
			slot := slices.Index(parent.attributes, key.attribute)
			clone.columns[j].Usage = parent.columns[slot].Usage
			clone.columns[j].OriginTable = uint32(id)
		}
		if err := stage.Create(&clone); err != nil {
			return nil, err
		}
	}
	if next == nil {
		return parent, nil
	}
	if err := stage.Replace(next); err != nil {
		return nil, err
	}
	return next, nil
}

// TYPE must fail at Execute, including private writes. Merge overlay successors
// before building a real unique root; this is not a commit-only integrity fence.
func validateAlterUniques(ctx context.Context, auth AuthorizationView, ref ObjectRef, parent *relationDescriptor, store *relationStorage, writes []rowVersion, deletes *privateDeletionRoot, schema string, output sqltype.OutputEnvironment, transformations []columnTransformation) ([]*logicalIndexRoot, error) {
	affected := false
	for _, c := range parent.uniqueConstraints {
		if uniqueTypeAffected(c, transformations) {
			affected = true
			break
		}
	}
	if !affected {
		return nil, nil
	}
	var baselines []*logicalIndexRoot
	if len(writes) == 0 && (deletes == nil || len(deletes.rows) == 0) {
		baselines = make([]*logicalIndexRoot, len(parent.uniqueConstraints))
	}
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
	candidate := relationStorage{versions: live}
	for i := range parent.uniqueConstraints {
		if !uniqueTypeAffected(parent.uniqueConstraints[i], transformations) {
			continue
		}
		c := &parent.uniqueConstraints[i]
		d := indexDefinition{parent: parent.address, unique: c, keys: make([]indexKey, len(c.Attributes))}
		for j, attr := range c.Attributes {
			d.keys[j] = indexKey{attribute: attr}
		}
		root, err := buildIndexRoot(ctx, parent, &d, &candidate)
		if err != nil {
			return nil, err
		}
		ordering, err := bindIndexOrdering(parent, &d)
		if err != nil {
			return nil, err
		}
		for j := 1; j < len(root.entries); j++ {
			key := root.entries[j].keys
			if !c.NullsNotDistinct && uniqueHasNull(key) {
				continue
			}
			cmp, err := ordering.compareKeys(ctx, root.entries[j-1], root.entries[j])
			if err != nil {
				return nil, err
			}
			if cmp == 0 {
				allowed, err := uniqueKeyDetailAllowed(auth, ref, c)
				if err != nil {
					return nil, err
				}
				return nil, uniqueDiagnostic(ctx, parent, c, key, schema, output, allowed, true)
			}
		}
		if baselines != nil {
			baselines[i] = root
		}
	}
	return baselines, nil
}

// The existing private catalog owner, not a statement/prepared binding, owns
// these immutable baseline roots. Clone/savepoint restoration shares roots.
func prepareAlterUniqueRoots(ctx context.Context, changes *catalogChanges, parent *relationDescriptor, baseline *relationStorage, validated []*logicalIndexRoot) error {
	for i, c := range parent.uniqueConstraints {
		original, private := changes.originals[c.Index]
		if !private || original != nil || changes.uniqueRoots[c.Index] != nil {
			continue
		}
		index, ok := changes.descriptors[c.Index].(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			return fmt.Errorf("stuntgres: private unique backing index is missing")
		}
		var root *logicalIndexRoot
		if validated != nil {
			root = validated[i]
		}
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
		changes.uniqueRoots[c.Index] = root
	}
	return nil
}
