package engine

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
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
)

// StageLikeColumns copies the identity, DEFAULT, generation and comment
// properties LIKE asks for into destination.
func StageLikeColumns(h table.Host, auth ddl.Authority, destination *catalog.Relation, inputs []analyze.LikeInput) error {
	view := h.AccessView(auth)
	for i := range inputs {
		in := &inputs[i]
		h.TableStage().CaptureLikeSource(in.Source)
		if in.SourceType != nil {
			h.AddDomainBase(in.SourceType)
		}
		for slot, attribute := range in.Source.Attributes {
			target := destination.Attributes[in.TargetSlot(slot)]
			if in.Options&analyze.LikeIdentity != 0 {
				if identity, ok := in.Source.ColumnMeta.Identities[attribute]; ok {
					sequence, ok := view.Catalog.Relation(identity.Sequence)
					if !ok || sequence.Sequence == nil {
						return fmt.Errorf("stuntgres: missing LIKE identity provider")
					}
					h.TableStage().CaptureLikeSource(sequence)
					options := *sequence.Sequence
					options.Persistence = "logged"
					read := h.StageRead()
					name := ownedSequenceName(destination.Name(), destination.Columns[in.TargetSlot(slot)].Name, func(n string) bool {
						return n == destination.Name() || len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination.Namespace(), Name: n})) != 0
					})
					if err := StageIdentitySequence(h, destination, in.TargetSlot(slot), name, identity.Mode, options, nil); err != nil {
						return err
					}
				}
			}
			if in.Options&analyze.LikeDefaults != 0 {
				if old, ok := in.Source.ColumnMeta.Defaults[attribute]; ok {
					// DEFAULT providers cannot reference the owner row; the cooked immutable
					// tree, including frozen literals and external identities, is reusable.
					node := old.Expression
					temporary := view.Catalog.MustNamespace(destination.Namespace()).Temporary
					if err := table.ValidateCookedDefaultTemporary(node, temporary); err != nil {
						return err
					}
					definition := catalog.ColumnDefault{Expression: node}
					if _, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Provenance: in.Source, CapturedManifest: old.Dependencies, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot, CatalogAliasReferences: &definition.CatalogAliasReferences}).SQL(h.Context(), view.Catalog, node); err != nil {
						return err
					}
					// Validate the original published recipe first. Old callable
					// identities may be copied unchanged only into its same physical
					// TEMP lifetime, not certified through an unpublished destination.
					for _, dependency := range old.Dependencies {
						if dependency.Address.ClassID != ids.ProcedureClassID || dependency.Epoch == view.Catalog.Epoch() {
							continue
						}
						sourceNamespace := view.Catalog.MustNamespace(in.Source.Namespace())
						if temporary == (ids.SessionToken{}) || temporary != sourceNamespace.Temporary || temporary.Domain() != view.Owner.Domain {
							return fmt.Errorf("stuntgres: stale stored routine reference")
						}
					}
					if destination.ColumnMeta.Defaults == nil {
						destination.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault)
					}
					destination.ColumnMeta.Defaults[target] = definition
					if destination.Inheritance != nil {
						provenance := destination.Inheritance.Columns[target]
						provenance.ExplicitDefault = true
						destination.Inheritance.Columns[target] = provenance
					}
				}
			}
			if in.Options&analyze.LikeGenerated != 0 {
				if old, ok := in.Source.ColumnMeta.Generations[attribute]; ok {
					if _, inherited := destination.ColumnMeta.Generations[target]; inherited {
						diagnostic := sqlError("2BP01", fmt.Sprintf("cannot drop default value for column %s of table %s because column %s of table %s requires it", destination.Columns[in.TargetSlot(slot)].Name, destination.Name(), destination.Columns[in.TargetSlot(slot)].Name, destination.Name())).(*sqlstate.Error)
						diagnostic.Hint = fmt.Sprintf("You can drop column %s of table %s instead.", destination.Columns[in.TargetSlot(slot)].Name, destination.Name())
						return diagnostic
					}
					node, err := stored.RemapLikeExpression(in, old.Expression, destination, view.Catalog)
					if err != nil {
						return err
					}
					planned, err := stored.RemapLikeExpression(in, old.Planned, destination, view.Catalog)
					if err != nil {
						return err
					}
					definition := catalog.ColumnGeneration{Kind: old.Kind, Expression: node, Planned: planned}
					if _, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: destination, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot}).SQL(h.Context(), view.Catalog, node); err != nil {
						return err
					}
					if destination.ColumnMeta.Generations == nil {
						destination.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration)
					}
					destination.ColumnMeta.Generations[target] = definition
				}
			}
			if in.Options&analyze.LikeComments != 0 {
				if comment, ok := in.Source.ColumnMeta.Comments[attribute]; ok {
					if destination.ColumnMeta.Comments == nil {
						destination.ColumnMeta.Comments = make(map[int32]string)
					}
					destination.ColumnMeta.Comments[target] = comment
				}
			}
		}
	}
	return nil
}

// StageLikeChecks copies the CHECK constraints of LIKE ... INCLUDING
// CONSTRAINTS sources into destination.
func StageLikeChecks(h table.Host, auth ddl.Authority, destination *catalog.Relation, inputs []analyze.LikeInput) error {
	view := h.AccessView(auth)
	names := make(map[string]bool)
	ordinal := 0
	for _, constraint := range destination.Constraints.NotNull {
		names[constraint.Name] = true
	}
	for _, constraint := range destination.Constraints.Check {
		names[constraint.Name] = true
		if constraint.Ordinal >= ordinal {
			ordinal = constraint.Ordinal + 1
		}
	}
	for i := range inputs {
		in := &inputs[i]
		if in.Options&analyze.LikeConstraints == 0 {
			continue
		}
		for _, old := range table.OrderedChecks(in.Source) {
			if names[old.Name] {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, old.Name, destination.Name()))
			}
			if ordinal == int(^uint(0)>>1) {
				return fmt.Errorf("stuntgres: CHECK ordinal exhausted")
			}
			node, err := stored.RemapLikeExpression(in, old.Expression, destination, view.Catalog)
			if err != nil {
				return err
			}
			definition := catalog.CheckConstraint{Name: old.Name, NoInherit: old.NoInherit, Ordinal: ordinal, Expression: node}
			var dependencies []ids.ObjectRef
			if _, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: destination, Dependencies: &dependencies, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(h.Context(), view.Catalog, node); err != nil {
				return err
			}
			for _, ref := range dependencies {
				address := ref.Address
				address.SubID = 0
				revision := destination.Revision()
				if provider, ok := view.Catalog.Descriptor(address); ok {
					revision = provider.Revision()
				}
				definition.Dependencies = append(definition.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: revision})
			}
			destination.Constraints.Check = append(destination.Constraints.Check, definition)
			if destination.Inheritance != nil {
				destination.Inheritance.Checks[definition.Name] = catalog.InheritedConstraint{Local: true}
			}
			names[old.Name] = true
			ordinal++
		}
	}
	slices.SortFunc(destination.Constraints.Check, func(a, b catalog.CheckConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}

// StageLikeIndexes copies the live indexes of LIKE ... INCLUDING INDEXES
// sources into destination. Clone live definitions, not source roots or validity flags. Invalid concurrent
// source indexes are rebuilt as ordinary ready/valid/live destination indexes.
func StageLikeIndexes(h table.Host, auth ddl.Authority, destination *catalog.Relation, inputs []analyze.LikeInput) error {
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
