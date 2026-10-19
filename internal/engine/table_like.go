package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type tableLikeMask uint16

const (
	likeComments tableLikeMask = 1 << iota
	likeCompression
	likeConstraints
	likeDefaults
	likeGenerated
	likeIdentity
	likeIndexes
	likeStatistics
	likeStorage
	likeAll = (1 << 9) - 1
)

func tableLikeOptions(options []parser.LikeOption) tableLikeMask {
	var mask tableLikeMask
	for _, option := range options {
		var bit tableLikeMask
		switch strings.ToLower(option.Name) {
		case "comments":
			bit = likeComments
		case "compression":
			bit = likeCompression
		case "constraints":
			bit = likeConstraints
		case "defaults":
			bit = likeDefaults
		case "generated":
			bit = likeGenerated
		case "identity":
			bit = likeIdentity
		case "indexes":
			bit = likeIndexes
		case "statistics":
			bit = likeStatistics
		case "storage":
			bit = likeStorage
		case "all":
			bit = likeAll
		}
		if option.Including {
			mask |= bit
		} else {
			mask &^= bit
		}
	}
	return mask
}

type createLikeInput struct {
	ref         ids.ObjectRef
	source      *catalog.Relation
	sourceType  *catalog.Type
	offset      int
	targetSlots []int
	options     tableLikeMask
	rowRef      func(ids.ObjectRef) ids.ObjectRef
	slots       map[int32]int
}

func (in createLikeInput) targetSlot(slot int) int {
	if in.targetSlots != nil {
		return in.targetSlots[slot]
	}
	return in.offset + slot
}

// Captures are transient CREATE inputs. Source rows, authority and object-literal
// references are never imported; row owners and stable attributes are mapped.
func (in createLikeInput) rowMapper(destination *catalog.Relation, authority catalog.Read) (func(ids.ObjectRef) ids.ObjectRef, map[int32]int) {
	attributes := make(map[int32]int32, len(in.source.Attributes))
	slots := make(map[int32]int, len(in.source.Attributes))
	for slot, attribute := range in.source.Attributes {
		target := destination.Attributes[in.targetSlot(slot)]
		attributes[attribute] = target
		slots[target] = in.targetSlot(slot)
	}
	// Retained TEMP source rows can carry an older epoch. Only mapped local
	// row origins receive the fresh CREATE candidate's reference authority.
	target := authority.Owner().Domain.Ref(authority.Epoch(), destination.Address())
	return func(ref ids.ObjectRef) ids.ObjectRef {
		address := ref.Address
		address.SubID = 0
		if address == in.source.Address() {
			attribute := ref.Address.SubID
			ref = target
			ref.Address.SubID = attributes[attribute]
		}
		return ref
	}, slots
}

func (in *createLikeInput) remap(node expr.Expr, destination *catalog.Relation, authority catalog.Read) (expr.Expr, error) {
	if in.rowRef == nil {
		in.rowRef, in.slots = in.rowMapper(destination, authority)
	}
	return remapScalarExpression(node, nil, in.rowRef, in.slots, nil)
}

func (s *CatalogStage) captureLikeSource(source catalog.Descriptor) {
	if !slices.Contains(s.likeSources, source) {
		s.likeSources = append(s.likeSources, source)
	}
}

func (s *CatalogStage) validateLikeSources(current catalog.Read) error {
	if len(s.likeSources) == 0 {
		return nil
	}
	if current.Epoch() != s.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, source := range s.likeSources {
		address := source.Address()
		if original, incumbent := s.Base.Find(address); incumbent {
			if original != source || current.Get(address) != source {
				return ids.ErrCatalogReplaced
			}
		} else {
			staged, ok := s.Descriptors[address].(*catalog.Relation)
			captured, sourceOK := source.(*catalog.Relation)
			// Private index-history normalization may replace the pointer while
			// retaining the staged incarnation and definition revision.
			if !ok || !sourceOK || staged.Revision() != source.Revision() || staged.RelationKind != captured.RelationKind {
				return fmt.Errorf("stuntgres: invalid staged LIKE source")
			}
		}
	}
	return nil
}

func (e *Engine) stageLikeColumns(op *Operation, auth AuthorizationView, stage *CatalogStage, destination *catalog.Relation, inputs []createLikeInput) error {
	for i := range inputs {
		in := &inputs[i]
		stage.captureLikeSource(in.source)
		if in.sourceType != nil {
			stage.domainBases = append(stage.domainBases, in.sourceType)
		}
		for slot, attribute := range in.source.Attributes {
			target := destination.Attributes[in.targetSlot(slot)]
			if in.options&likeIdentity != 0 {
				if identity, ok := in.source.ColumnMeta.Identities[attribute]; ok {
					sequence, ok := auth.catalog.Relation(identity.Sequence)
					if !ok || sequence.Sequence == nil {
						return fmt.Errorf("stuntgres: missing LIKE identity provider")
					}
					stage.captureLikeSource(sequence)
					options := *sequence.Sequence
					options.Persistence = "logged"
					read := e.stageRead(stage)
					name := ownedSequenceName(destination.Name(), destination.Columns[in.targetSlot(slot)].Name, func(n string) bool {
						return n == destination.Name() || len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination.Namespace(), Name: n})) != 0
					})
					if err := e.stageIdentitySequence(stage, destination, in.targetSlot(slot), name, identity.Mode, options, nil); err != nil {
						return err
					}
				}
			}
			if in.options&likeDefaults != 0 {
				if old, ok := in.source.ColumnMeta.Defaults[attribute]; ok {
					// DEFAULT providers cannot reference the owner row; the cooked immutable
					// tree, including frozen literals and external identities, is reusable.
					node := old.Expression
					temporary := auth.catalog.MustNamespace(destination.Namespace()).Temporary
					if err := validateCookedDefaultTemporary(node, temporary); err != nil {
						return err
					}
					definition := catalog.ColumnDefault{Expression: node}
					if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, provenance: in.source, capturedManifest: old.Dependencies, dependencies: &definition.Dependencies, commentSnapshot: &definition.CatalogSnapshot, catalogAliasReferences: &definition.CatalogAliasReferences}).sql(op.Context(), auth.catalog, node); err != nil {
						return err
					}
					// Validate the original published recipe first. Old callable
					// identities may be copied unchanged only into its same physical
					// TEMP lifetime, not certified through an unpublished destination.
					for _, dependency := range old.Dependencies {
						if dependency.Address.ClassID != ids.ProcedureClassID || dependency.Epoch == auth.catalog.Epoch() {
							continue
						}
						sourceNamespace := auth.catalog.MustNamespace(in.source.Namespace())
						if temporary == (ids.SessionToken{}) || temporary != sourceNamespace.Temporary || temporary.Domain() != domainOf(e) {
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
			if in.options&likeGenerated != 0 {
				if old, ok := in.source.ColumnMeta.Generations[attribute]; ok {
					if _, inherited := destination.ColumnMeta.Generations[target]; inherited {
						diagnostic := sqlError("2BP01", fmt.Sprintf("cannot drop default value for column %s of table %s because column %s of table %s requires it", destination.Columns[in.targetSlot(slot)].Name, destination.Name(), destination.Columns[in.targetSlot(slot)].Name, destination.Name())).(*sqlstate.Error)
						diagnostic.Hint = fmt.Sprintf("You can drop column %s of table %s instead.", destination.Columns[in.targetSlot(slot)].Name, destination.Name())
						return diagnostic
					}
					node, err := in.remap(old.Expression, destination, auth.catalog)
					if err != nil {
						return err
					}
					planned, err := in.remap(old.Planned, destination, auth.catalog)
					if err != nil {
						return err
					}
					definition := catalog.ColumnGeneration{Kind: old.Kind, Expression: node, Planned: planned}
					if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: destination, dependencies: &definition.Dependencies, commentSnapshot: &definition.CatalogSnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
						return err
					}
					if destination.ColumnMeta.Generations == nil {
						destination.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration)
					}
					destination.ColumnMeta.Generations[target] = definition
				}
			}
			if in.options&likeComments != 0 {
				if comment, ok := in.source.ColumnMeta.Comments[attribute]; ok {
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

func (e *Engine) stageLikeChecks(op *Operation, auth AuthorizationView, destination *catalog.Relation, inputs []createLikeInput) error {
	names := make(map[string]bool)
	ordinal := 0
	for _, constraint := range destination.NotNullConstraints {
		names[constraint.Name] = true
	}
	for _, constraint := range destination.CheckConstraints {
		names[constraint.Name] = true
		if constraint.Ordinal >= ordinal {
			ordinal = constraint.Ordinal + 1
		}
	}
	for i := range inputs {
		in := &inputs[i]
		if in.options&likeConstraints == 0 {
			continue
		}
		for _, old := range orderedChecks(in.source) {
			if names[old.Name] {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, old.Name, destination.Name()))
			}
			if ordinal == int(^uint(0)>>1) {
				return fmt.Errorf("stuntgres: CHECK ordinal exhausted")
			}
			node, err := in.remap(old.Expression, destination, auth.catalog)
			if err != nil {
				return err
			}
			definition := catalog.CheckConstraint{Name: old.Name, NoInherit: old.NoInherit, Ordinal: ordinal, Expression: node}
			var dependencies []ids.ObjectRef
			if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: destination, dependencies: &dependencies, commentSnapshot: &definition.CommentSnapshot, visibilitySnapshot: &definition.VisibilitySnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
				return err
			}
			for _, ref := range dependencies {
				address := ref.Address
				address.SubID = 0
				revision := destination.Revision()
				if provider, ok := auth.catalog.Descriptor(address); ok {
					revision = provider.Revision()
				}
				definition.Dependencies = append(definition.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: revision})
			}
			destination.CheckConstraints = append(destination.CheckConstraints, definition)
			if destination.Inheritance != nil {
				destination.Inheritance.Checks[definition.Name] = catalog.InheritedConstraint{Local: true}
			}
			names[old.Name] = true
			ordinal++
		}
	}
	slices.SortFunc(destination.CheckConstraints, func(a, b catalog.CheckConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}
