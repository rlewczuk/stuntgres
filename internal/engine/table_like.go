package engine

import (
	"fmt"
	"slices"
	"strings"
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
	ref         ObjectRef
	source      *relationDescriptor
	sourceType  *typeDescriptor
	offset      int
	targetSlots []int
	options     tableLikeMask
	rowRef      func(ObjectRef) ObjectRef
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
func (in createLikeInput) rowMapper(destination *relationDescriptor, authority CatalogRead) (func(ObjectRef) ObjectRef, map[int32]int) {
	attributes := make(map[int32]int32, len(in.source.attributes))
	slots := make(map[int32]int, len(in.source.attributes))
	for slot, attribute := range in.source.attributes {
		target := destination.attributes[in.targetSlot(slot)]
		attributes[attribute] = target
		slots[target] = in.targetSlot(slot)
	}
	// Retained TEMP source rows can carry an older epoch. Only mapped local
	// row origins receive the fresh CREATE candidate's reference authority.
	target := ObjectRef{owner: authority.owner.domain, Epoch: authority.Epoch(), Address: destination.address}
	return func(ref ObjectRef) ObjectRef {
		address := ref.Address
		address.SubID = 0
		if address == in.source.address {
			attribute := ref.Address.SubID
			ref = target
			ref.Address.SubID = attributes[attribute]
		}
		return ref
	}, slots
}

func (in *createLikeInput) remap(node BoundExpr, destination *relationDescriptor, authority CatalogRead) (BoundExpr, error) {
	if in.rowRef == nil {
		in.rowRef, in.slots = in.rowMapper(destination, authority)
	}
	return remapScalarExpression(node, nil, in.rowRef, in.slots, nil)
}

func (s *CatalogStage) captureLikeSource(source Descriptor) {
	if !slices.Contains(s.likeSources, source) {
		s.likeSources = append(s.likeSources, source)
	}
}

func (s *CatalogStage) validateLikeSources(current CatalogRead) error {
	if len(s.likeSources) == 0 {
		return nil
	}
	if current.Epoch() != s.base.Epoch() {
		return ErrCatalogReplaced
	}
	for _, source := range s.likeSources {
		address := source.Address()
		if original, incumbent := s.base.state.descriptors[address]; incumbent {
			if original != source || current.state.descriptors[address] != source {
				return ErrCatalogReplaced
			}
		} else {
			staged, ok := s.descriptors[address].(*relationDescriptor)
			captured, sourceOK := source.(*relationDescriptor)
			// Private index-history normalization may replace the pointer while
			// retaining the staged incarnation and definition revision.
			if !ok || !sourceOK || staged.Revision() != source.Revision() || staged.relationKind != captured.relationKind {
				return fmt.Errorf("stuntgres: invalid staged LIKE source")
			}
		}
	}
	return nil
}

func (e *Engine) stageLikeColumns(op *Operation, auth AuthorizationView, stage *CatalogStage, destination *relationDescriptor, inputs []createLikeInput) error {
	for i := range inputs {
		in := &inputs[i]
		stage.captureLikeSource(in.source)
		if in.sourceType != nil {
			stage.domainBases = append(stage.domainBases, in.sourceType)
		}
		for slot, attribute := range in.source.attributes {
			target := destination.attributes[in.targetSlot(slot)]
			if in.options&likeIdentity != 0 {
				if identity, ok := in.source.identities[attribute]; ok {
					sequence, ok := auth.catalog.state.descriptors[identity.sequence].(*relationDescriptor)
					if !ok || sequence.sequence == nil {
						return fmt.Errorf("stuntgres: missing LIKE identity provider")
					}
					stage.captureLikeSource(sequence)
					options := *sequence.sequence
					options.persistence = "logged"
					read := e.stageRead(stage)
					name := ownedSequenceName(destination.name, destination.columns[in.targetSlot(slot)].Name, func(n string) bool {
						return n == destination.name || len(read.Lookup(NameKey{ClassID: RelationClassID, Namespace: destination.namespace, Name: n})) != 0
					})
					if err := e.stageIdentitySequence(stage, destination, in.targetSlot(slot), name, identity.mode, options, nil); err != nil {
						return err
					}
				}
			}
			if in.options&likeDefaults != 0 {
				if old, ok := in.source.defaults[attribute]; ok {
					// DEFAULT providers cannot reference the owner row; the cooked immutable
					// tree, including frozen literals and external identities, is reusable.
					node := old.expression
					temporary := auth.catalog.state.descriptors[destination.namespace].(*namespaceDescriptor).temporary
					if err := validateCookedDefaultTemporary(node, temporary); err != nil {
						return err
					}
					definition := columnDefault{expression: node}
					if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, provenance: in.source, capturedManifest: old.dependencies, dependencies: &definition.dependencies, commentSnapshot: &definition.catalogSnapshot, catalogAliasReferences: &definition.catalogAliasReferences}).sql(op.Context(), auth.catalog, node); err != nil {
						return err
					}
					// Validate the original published recipe first. Old callable
					// identities may be copied unchanged only into its same physical
					// TEMP lifetime, not certified through an unpublished destination.
					for _, dependency := range old.dependencies {
						if dependency.Address.ClassID != ProcedureClassID || dependency.Epoch == auth.catalog.Epoch() {
							continue
						}
						sourceNamespace := auth.catalog.state.descriptors[in.source.namespace].(*namespaceDescriptor)
						if temporary == (SessionToken{}) || temporary != sourceNamespace.temporary || temporary.domain != domainOf(e) {
							return fmt.Errorf("stuntgres: stale stored routine reference")
						}
					}
					if destination.defaults == nil {
						destination.defaults = make(map[int32]columnDefault)
					}
					destination.defaults[target] = definition
					if destination.inheritance != nil {
						provenance := destination.inheritance.columns[target]
						provenance.explicitDefault = true
						destination.inheritance.columns[target] = provenance
					}
				}
			}
			if in.options&likeGenerated != 0 {
				if old, ok := in.source.generations[attribute]; ok {
					if _, inherited := destination.generations[target]; inherited {
						diagnostic := sqlError("2BP01", fmt.Sprintf("cannot drop default value for column %s of table %s because column %s of table %s requires it", destination.columns[in.targetSlot(slot)].Name, destination.name, destination.columns[in.targetSlot(slot)].Name, destination.name)).(*sqlstate.Error)
						diagnostic.Hint = fmt.Sprintf("You can drop column %s of table %s instead.", destination.columns[in.targetSlot(slot)].Name, destination.name)
						return diagnostic
					}
					node, err := in.remap(old.expression, destination, auth.catalog)
					if err != nil {
						return err
					}
					planned, err := in.remap(old.planned, destination, auth.catalog)
					if err != nil {
						return err
					}
					definition := columnGeneration{kind: old.kind, expression: node, planned: planned}
					if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: destination, dependencies: &definition.dependencies, commentSnapshot: &definition.catalogSnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
						return err
					}
					if destination.generations == nil {
						destination.generations = make(map[int32]columnGeneration)
					}
					destination.generations[target] = definition
				}
			}
			if in.options&likeComments != 0 {
				if comment, ok := in.source.columnComments[attribute]; ok {
					if destination.columnComments == nil {
						destination.columnComments = make(map[int32]string)
					}
					destination.columnComments[target] = comment
				}
			}
		}
	}
	return nil
}

func (e *Engine) stageLikeChecks(op *Operation, auth AuthorizationView, destination *relationDescriptor, inputs []createLikeInput) error {
	names := make(map[string]bool)
	ordinal := 0
	for _, constraint := range destination.notNullConstraints {
		names[constraint.Name] = true
	}
	for _, constraint := range destination.checkConstraints {
		names[constraint.Name] = true
		if constraint.ordinal >= ordinal {
			ordinal = constraint.ordinal + 1
		}
	}
	for i := range inputs {
		in := &inputs[i]
		if in.options&likeConstraints == 0 {
			continue
		}
		for _, old := range orderedChecks(in.source) {
			if names[old.Name] {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, old.Name, destination.name))
			}
			if ordinal == int(^uint(0)>>1) {
				return fmt.Errorf("stuntgres: CHECK ordinal exhausted")
			}
			node, err := in.remap(old.expression, destination, auth.catalog)
			if err != nil {
				return err
			}
			definition := checkConstraint{Name: old.Name, NoInherit: old.NoInherit, ordinal: ordinal, expression: node}
			var dependencies []ObjectRef
			if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: destination, dependencies: &dependencies, commentSnapshot: &definition.commentSnapshot, visibilitySnapshot: &definition.visibilitySnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
				return err
			}
			for _, ref := range dependencies {
				address := ref.Address
				address.SubID = 0
				revision := destination.revision
				if provider, ok := auth.catalog.Descriptor(address); ok {
					revision = provider.Revision()
				}
				definition.dependencies = append(definition.dependencies, expressionDependency{ref: ref, revision: revision})
			}
			destination.checkConstraints = append(destination.checkConstraints, definition)
			if destination.inheritance != nil {
				destination.inheritance.checks[definition.Name] = inheritedConstraint{local: true}
			}
			names[old.Name] = true
			ordinal++
		}
	}
	slices.SortFunc(destination.checkConstraints, func(a, b checkConstraint) int { return strings.Compare(a.Name, b.Name) })
	return nil
}
