package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (in createLikeInput) attribute(destination *catalog.Relation, old int32) (int32, error) {
	slot := slices.Index(in.source.Attributes, old)
	if slot < 0 {
		return 0, fmt.Errorf("stuntgres: missing LIKE index attribute")
	}
	return destination.Attributes[in.targetSlot(slot)], nil
}

// Clone live definitions, not source roots or validity flags. Invalid concurrent
// source indexes are rebuilt as ordinary ready/valid/live destination indexes.
func (e *Engine) stageLikeIndexes(op *Operation, auth AuthorizationView, stage *CatalogStage, destination *catalog.Relation, inputs []createLikeInput) error {
	for i := range inputs {
		in := &inputs[i]
		if in.options&likeIndexes == 0 {
			continue
		}
		var sources []*catalog.Relation
		for _, descriptor := range auth.catalog.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if ok && index.SQLIndex != nil && index.SQLIndex.Parent == in.source.Address() && index.SQLIndex.Live {
				sources = append(sources, index)
			}
		}
		slices.SortFunc(sources, func(a, b *catalog.Relation) int {
			if a.Address().ObjectID < b.Address().ObjectID {
				return -1
			}
			if a.Address().ObjectID > b.Address().ObjectID {
				return 1
			}
			return 0
		})
		for _, source := range sources {
			old := source.SQLIndex
			// Copied PK was bound and staged before explicit UNIQUE. Keep the
			// remaining independent LIKE index copies in source index order.
			if old.Constraint != nil && old.Constraint.Primary {
				continue
			}
			stage.captureLikeSource(source)
			definition := *old
			definition.Parent = destination.Address()
			definition.PartitionParent = ids.ObjectAddress{}
			definition.Ready, definition.Valid, definition.Live = true, true, true
			definition.Constraint = nil
			definition.Keys = slices.Clone(old.Keys)
			definition.Include = make([]int32, len(old.Include))
			for i, attribute := range old.Include {
				mapped, err := in.attribute(destination, attribute)
				if err != nil {
					return err
				}
				definition.Include[i] = mapped
			}
			for i := range definition.Keys {
				key := &definition.Keys[i]
				if key.Derived == nil {
					mapped, err := in.attribute(destination, key.Attribute)
					if err != nil {
						return err
					}
					key.Attribute = mapped
					continue
				}
				node, err := in.remap(key.Derived.Definition, destination, auth.catalog)
				if err != nil {
					return err
				}
				execution, err := in.remap(key.Derived.Execution, destination, auth.catalog)
				if err != nil {
					return err
				}
				derived := &catalog.DerivedIndexKey{Definition: node, Execution: execution, Attributes: destination.Attributes}
				if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: destination, dependencies: &derived.Dependencies}).sql(op.Context(), auth.catalog, node); err != nil {
					return err
				}
				key.Derived = derived
			}
			if old.Predicate != nil {
				node, err := in.remap(old.Predicate.Definition, destination, auth.catalog)
				if err != nil {
					return err
				}
				execution, err := in.remap(old.Predicate.Execution, destination, auth.catalog)
				if err != nil {
					return err
				}
				predicate := &catalog.IndexPredicate{Definition: node, Execution: execution, Attributes: destination.Attributes}
				// The analyzed tree retains external dependencies even when the
				// execution tree has folded their containing arm away.
				if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: destination, dependencies: &predicate.Dependencies}).sql(op.Context(), auth.catalog, node); err != nil {
					return err
				}
				definition.Predicate = predicate
			}
			labels := make([]string, len(source.Columns))
			for i, column := range source.Columns {
				labels[i] = column.Name
			}
			read := e.stageRead(stage)
			label := "idx"
			var constraints map[string]bool
			if old.Constraint != nil {
				label = "key"
				if old.IsExclusion() {
					label = "excl"
				}
				constraints = constraintNames(read, destination.Namespace(), destination)
			}
			name := generatedRelationName(destination.Name(), strings.Join(labels, "_"), label, func(name string) bool {
				return constraints[name] || name == destination.Name() || len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination.Namespace(), Name: name})) != 0
			})
			oid, err := e.catalog.allocateOIDLocked()
			if err != nil {
				return err
			}
			revision, err := e.catalog.revisionLocked()
			if err != nil {
				return err
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
			if old.Constraint != nil && old.Constraint.Kind == catalog.UniqueBackingKind {
				c := catalog.UniqueConstraint{Name: name, IsLocal: true, Primary: old.Constraint.Primary, NullsNotDistinct: old.NullsNotDistinct, Index: address, Attributes: make([]int32, len(old.Keys))}
				c.OID, err = e.catalog.allocateOIDLocked()
				if err != nil {
					return err
				}
				for i, key := range old.Keys {
					attribute, expression, keyErr := indexCatalogKey(in.source, key)
					if keyErr != nil || expression != nil {
						return fmt.Errorf("stuntgres: invalid LIKE unique key")
					}
					c.Attributes[i], err = in.attribute(destination, attribute)
					if err != nil {
						return err
					}
				}
				destination.UniqueConstraints = append(destination.UniqueConstraints, c)
				definition.Constraint = uniqueBacking(&c)
			}
			if old.IsExclusion() {
				original := exclusionDefinition(in.source, source)
				if original == nil {
					return fmt.Errorf("stuntgres: missing inherited exclusion owner")
				}
				c := *original
				c.Name, c.Index = name, address
				c.Parent, c.IsLocal = 0, true
				definition.Exclusion, err = rebindExclusionOperators(destination, &definition)
				if err != nil {
					return err
				}
				c.Operators = definition.Exclusion
				c.OID, err = e.catalog.allocateOIDLocked()
				if err != nil {
					return err
				}
				destination.ExclusionConstraints = append(destination.ExclusionConstraints, c)
				definition.Constraint = exclusionBacking(&c)
			}
			columns := slices.Clone(source.Columns)
			lookup := make(map[string]int, len(columns))
			for i := range columns {
				columns[i].OriginTable = uint32(oid)
				columns[i].OriginAttribute = int16(i + 1)
				lookup[columns[i].Name] = i
			}
			index := &catalog.Relation{ObjectAddress: address, ObjectNamespace: destination.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: destination.Owner, Columns: columns, Attributes: slices.Clone(source.Attributes), AttributeHighWater: source.AttributeHighWater, Index: lookup, RelationKind: catalog.IndexRelation, SQLIndex: &definition}
			if in.options&likeComments != 0 {
				index.Comment = source.Comment
			}
			if err := stage.Create(index); err != nil {
				return err
			}
		}
	}
	return nil
}
