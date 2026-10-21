package table

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
)

// StageLikeIndexes copies the live indexes of LIKE ... INCLUDING INDEXES
// sources into destination. Clone live definitions, not source roots or validity flags. Invalid concurrent
// source indexes are rebuilt as ordinary ready/valid/live destination indexes.
func StageLikeIndexes(h Host, auth ddl.Authority, destination *catalog.Relation, inputs []analyze.LikeInput) error {
	view := h.AccessView(auth)
	for i := range inputs {
		in := &inputs[i]
		if in.Options&analyze.LikeIndexes == 0 {
			continue
		}
		var sources []*catalog.Relation
		for _, descriptor := range view.Catalog.Descriptors() {
			index, ok := descriptor.(*catalog.Relation)
			if ok && index.SQLIndex != nil && index.SQLIndex.Parent == in.Source.Address() && index.SQLIndex.Live {
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
			h.TableStage().CaptureLikeSource(source)
			definition := *old
			definition.Parent = destination.Address()
			definition.PartitionParent = ids.ObjectAddress{}
			definition.Ready, definition.Valid, definition.Live = true, true, true
			definition.Constraint = nil
			definition.Keys = slices.Clone(old.Keys)
			definition.Include = make([]int32, len(old.Include))
			for i, attribute := range old.Include {
				mapped, err := in.Attribute(destination, attribute)
				if err != nil {
					return err
				}
				definition.Include[i] = mapped
			}
			for i := range definition.Keys {
				key := &definition.Keys[i]
				if key.Derived == nil {
					mapped, err := in.Attribute(destination, key.Attribute)
					if err != nil {
						return err
					}
					key.Attribute = mapped
					continue
				}
				node, err := stored.RemapLikeExpression(in, key.Derived.Definition, destination, view.Catalog)
				if err != nil {
					return err
				}
				execution, err := stored.RemapLikeExpression(in, key.Derived.Execution, destination, view.Catalog)
				if err != nil {
					return err
				}
				derived := &catalog.DerivedIndexKey{Definition: node, Execution: execution, Attributes: destination.Attributes}
				if _, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: destination, Dependencies: &derived.Dependencies}).SQL(h.Context(), view.Catalog, node); err != nil {
					return err
				}
				key.Derived = derived
			}
			if old.Predicate != nil {
				node, err := stored.RemapLikeExpression(in, old.Predicate.Definition, destination, view.Catalog)
				if err != nil {
					return err
				}
				execution, err := stored.RemapLikeExpression(in, old.Predicate.Execution, destination, view.Catalog)
				if err != nil {
					return err
				}
				predicate := &catalog.IndexPredicate{Definition: node, Execution: execution, Attributes: destination.Attributes}
				// The analyzed tree retains external dependencies even when the
				// execution tree has folded their containing arm away.
				if _, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: destination, Dependencies: &predicate.Dependencies}).SQL(h.Context(), view.Catalog, node); err != nil {
					return err
				}
				definition.Predicate = predicate
			}
			labels := make([]string, len(source.Columns))
			for i, column := range source.Columns {
				labels[i] = column.Name
			}
			read := h.StageRead()
			label := "idx"
			var constraints map[string]bool
			if old.Constraint != nil {
				label = "key"
				if old.IsExclusion() {
					label = "excl"
				}
				constraints = catalog.ConstraintNames(read, destination.Namespace(), destination)
			}
			name := ddl.GeneratedRelationName(destination.Name(), strings.Join(labels, "_"), label, func(name string) bool {
				return constraints[name] || name == destination.Name() || len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination.Namespace(), Name: name})) != 0
			})
			oid, err := h.AllocateOID()
			if err != nil {
				return err
			}
			revision, err := h.Revision()
			if err != nil {
				return err
			}
			address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
			if old.Constraint != nil && old.Constraint.Kind == catalog.UniqueBackingKind {
				c := catalog.UniqueConstraint{Name: name, IsLocal: true, Primary: old.Constraint.Primary, NullsNotDistinct: old.NullsNotDistinct, Index: address, Attributes: make([]int32, len(old.Keys))}
				c.OID, err = h.AllocateOID()
				if err != nil {
					return err
				}
				for i, key := range old.Keys {
					attribute, expression, keyErr := index.CatalogKey(in.Source, key)
					if keyErr != nil || expression != nil {
						return fmt.Errorf("stuntgres: invalid LIKE unique key")
					}
					c.Attributes[i], err = in.Attribute(destination, attribute)
					if err != nil {
						return err
					}
				}
				destination.Constraints.Unique = append(destination.Constraints.Unique, c)
				definition.Constraint = index.UniqueBacking(&c)
			}
			if old.IsExclusion() {
				original := index.ExclusionDefinition(in.Source, source)
				if original == nil {
					return fmt.Errorf("stuntgres: missing inherited exclusion owner")
				}
				c := *original
				c.Name, c.Index = name, address
				c.Parent, c.IsLocal = 0, true
				definition.Exclusion, err = index.RebindExclusionOperators(destination, &definition)
				if err != nil {
					return err
				}
				c.Operators = definition.Exclusion
				c.OID, err = h.AllocateOID()
				if err != nil {
					return err
				}
				destination.Constraints.Exclusion = append(destination.Constraints.Exclusion, c)
				definition.Constraint = index.ExclusionBacking(&c)
			}
			columns := slices.Clone(source.Columns)
			lookup := make(map[string]int, len(columns))
			for i := range columns {
				columns[i].OriginTable = uint32(oid)
				columns[i].OriginAttribute = int16(i + 1)
				lookup[columns[i].Name] = i
			}
			index := &catalog.Relation{ObjectAddress: address, ObjectNamespace: destination.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: destination.Owner, Columns: columns, Attributes: slices.Clone(source.Attributes), ColumnMeta: catalog.ColumnMetadata{HighWater: source.ColumnMeta.HighWater}, Index: lookup, RelationKind: catalog.IndexRelation, SQLIndex: &definition}
			if in.Options&analyze.LikeComments != 0 {
				index.Comment = source.Comment
			}
			if err := h.Stage().Create(index); err != nil {
				return err
			}
		}
	}
	return nil
}
