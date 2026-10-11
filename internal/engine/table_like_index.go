package engine

import (
	"fmt"
	"slices"
	"strings"
)

func (in createLikeInput) attribute(destination *relationDescriptor, old int32) (int32, error) {
	slot := slices.Index(in.source.attributes, old)
	if slot < 0 {
		return 0, fmt.Errorf("stuntgres: missing LIKE index attribute")
	}
	return destination.attributes[in.targetSlot(slot)], nil
}

// Clone live definitions, not source roots or validity flags. Invalid concurrent
// source indexes are rebuilt as ordinary ready/valid/live destination indexes.
func (e *Engine) stageLikeIndexes(op *Operation, auth AuthorizationView, stage *CatalogStage, destination *relationDescriptor, inputs []createLikeInput) error {
	for i := range inputs {
		in := &inputs[i]
		if in.options&likeIndexes == 0 {
			continue
		}
		var sources []*relationDescriptor
		for _, descriptor := range auth.catalog.state.descriptors {
			index, ok := descriptor.(*relationDescriptor)
			if ok && index.sqlIndex != nil && index.sqlIndex.parent == in.source.address && index.sqlIndex.live {
				sources = append(sources, index)
			}
		}
		slices.SortFunc(sources, func(a, b *relationDescriptor) int {
			if a.address.ObjectID < b.address.ObjectID {
				return -1
			}
			if a.address.ObjectID > b.address.ObjectID {
				return 1
			}
			return 0
		})
		for _, source := range sources {
			old := source.sqlIndex
			// Copied PK was bound and staged before explicit UNIQUE. Keep the
			// remaining independent LIKE index copies in source index order.
			if old.constraint != nil && old.constraint.Primary {
				continue
			}
			stage.captureLikeSource(source)
			definition := *old
			definition.parent = destination.address
			definition.partitionParent = ObjectAddress{}
			definition.ready, definition.valid, definition.live = true, true, true
			definition.constraint = nil
			definition.keys = slices.Clone(old.keys)
			definition.include = make([]int32, len(old.include))
			for i, attribute := range old.include {
				mapped, err := in.attribute(destination, attribute)
				if err != nil {
					return err
				}
				definition.include[i] = mapped
			}
			for i := range definition.keys {
				key := &definition.keys[i]
				if key.derived == nil {
					mapped, err := in.attribute(destination, key.attribute)
					if err != nil {
						return err
					}
					key.attribute = mapped
					continue
				}
				node, err := in.remap(key.derived.definition, destination, auth.catalog)
				if err != nil {
					return err
				}
				execution, err := in.remap(key.derived.execution, destination, auth.catalog)
				if err != nil {
					return err
				}
				derived := &derivedIndexKey{definition: node, execution: execution, attributes: destination.attributes}
				if _, err := (scalarRenderer{owner: destination, dependencies: &derived.dependencies}).sql(op.Context(), auth.catalog, node); err != nil {
					return err
				}
				key.derived = derived
			}
			if old.predicate != nil {
				node, err := in.remap(old.predicate.definition, destination, auth.catalog)
				if err != nil {
					return err
				}
				execution, err := in.remap(old.predicate.execution, destination, auth.catalog)
				if err != nil {
					return err
				}
				predicate := &indexPredicate{definition: node, execution: execution, attributes: destination.attributes}
				// The analyzed tree retains external dependencies even when the
				// execution tree has folded their containing arm away.
				if _, err := (scalarRenderer{owner: destination, dependencies: &predicate.dependencies}).sql(op.Context(), auth.catalog, node); err != nil {
					return err
				}
				definition.predicate = predicate
			}
			labels := make([]string, len(source.columns))
			for i, column := range source.columns {
				labels[i] = column.Name
			}
			read := e.stageRead(stage)
			label := "idx"
			var constraints map[string]bool
			if old.constraint != nil {
				label = "key"
				if old.isExclusion() {
					label = "excl"
				}
				constraints = constraintNames(read, destination.namespace, destination)
			}
			name := generatedRelationName(destination.name, strings.Join(labels, "_"), label, func(name string) bool {
				return constraints[name] || name == destination.name || len(read.Lookup(NameKey{ClassID: RelationClassID, Namespace: destination.namespace, Name: name})) != 0
			})
			oid, err := e.allocateOIDLocked()
			if err != nil {
				return err
			}
			revision, err := e.revisionLocked()
			if err != nil {
				return err
			}
			address := ObjectAddress{ClassID: RelationClassID, ObjectID: oid}
			if old.constraint != nil && old.constraint.kind == uniqueBackingKind {
				c := uniqueConstraint{Name: name, IsLocal: true, Primary: old.constraint.Primary, NullsNotDistinct: old.nullsNotDistinct, Index: address, Attributes: make([]int32, len(old.keys))}
				c.OID, err = e.allocateOIDLocked()
				if err != nil {
					return err
				}
				for i, key := range old.keys {
					attribute, expression, keyErr := indexCatalogKey(in.source, key)
					if keyErr != nil || expression != nil {
						return fmt.Errorf("stuntgres: invalid LIKE unique key")
					}
					c.Attributes[i], err = in.attribute(destination, attribute)
					if err != nil {
						return err
					}
				}
				destination.uniqueConstraints = append(destination.uniqueConstraints, c)
				definition.constraint = uniqueBacking(&c)
			}
			if old.isExclusion() {
				original := exclusionDefinition(in.source, source)
				if original == nil {
					return fmt.Errorf("stuntgres: missing inherited exclusion owner")
				}
				c := *original
				c.Name, c.Index = name, address
				c.OID, err = e.allocateOIDLocked()
				if err != nil {
					return err
				}
				destination.exclusionConstraints = append(destination.exclusionConstraints, c)
				definition.constraint = exclusionBacking(&c)
			}
			columns := slices.Clone(source.columns)
			lookup := make(map[string]int, len(columns))
			for i := range columns {
				columns[i].OriginTable = uint32(oid)
				columns[i].OriginAttribute = int16(i + 1)
				lookup[columns[i].Name] = i
			}
			index := &relationDescriptor{address: address, namespace: destination.namespace, name: name, revision: revision, owner: destination.owner, columns: columns, attributes: slices.Clone(source.attributes), attributeHighWater: source.attributeHighWater, index: lookup, relationKind: indexRelation, sqlIndex: &definition}
			if in.options&likeComments != 0 {
				index.comment = source.comment
			}
			if err := stage.Create(index); err != nil {
				return err
			}
		}
	}
	return nil
}
