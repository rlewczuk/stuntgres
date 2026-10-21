package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
)

func (s *CatalogStage) captureLikeSource(source catalog.Descriptor) {
	if table := s.table(); !slices.Contains(table.likeSources, source) {
		table.likeSources = append(table.likeSources, source)
	}
}

func (s *CatalogStage) validateLikeSources(current catalog.Read) error {
	sources := s.tableOrZero().likeSources
	if len(sources) == 0 {
		return nil
	}
	if current.Epoch() != s.Base.Epoch() {
		return ids.ErrCatalogReplaced
	}
	for _, source := range sources {
		address := source.Address()
		if original, incumbent := s.Base.Find(address); incumbent {
			if original != source || current.Get(address) != source {
				return ids.ErrCatalogReplaced
			}
		} else {
			staged, ok := s.Get(address).(*catalog.Relation)
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

func (e *Engine) stageLikeColumns(op *session.Operation, auth AuthorizationView, stage *CatalogStage, destination *catalog.Relation, inputs []analyze.LikeInput) error {
	for i := range inputs {
		in := &inputs[i]
		stage.captureLikeSource(in.Source)
		if in.SourceType != nil {
			stage.domainBases = append(stage.domainBases, in.SourceType)
		}
		for slot, attribute := range in.Source.Attributes {
			target := destination.Attributes[in.TargetSlot(slot)]
			if in.Options&analyze.LikeIdentity != 0 {
				if identity, ok := in.Source.ColumnMeta.Identities[attribute]; ok {
					sequence, ok := auth.Catalog.Relation(identity.Sequence)
					if !ok || sequence.Sequence == nil {
						return fmt.Errorf("stuntgres: missing LIKE identity provider")
					}
					stage.captureLikeSource(sequence)
					options := *sequence.Sequence
					options.Persistence = "logged"
					read := e.stageRead(stage)
					name := ownedSequenceName(destination.Name(), destination.Columns[in.TargetSlot(slot)].Name, func(n string) bool {
						return n == destination.Name() || len(read.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: destination.Namespace(), Name: n})) != 0
					})
					if err := e.stageIdentitySequence(stage, destination, in.TargetSlot(slot), name, identity.Mode, options, nil); err != nil {
						return err
					}
				}
			}
			if in.Options&analyze.LikeDefaults != 0 {
				if old, ok := in.Source.ColumnMeta.Defaults[attribute]; ok {
					// DEFAULT providers cannot reference the owner row; the cooked immutable
					// tree, including frozen literals and external identities, is reusable.
					node := old.Expression
					temporary := auth.Catalog.MustNamespace(destination.Namespace()).Temporary
					if err := validateCookedDefaultTemporary(node, temporary); err != nil {
						return err
					}
					definition := catalog.ColumnDefault{Expression: node}
					if _, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Provenance: in.Source, CapturedManifest: old.Dependencies, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot, CatalogAliasReferences: &definition.CatalogAliasReferences}).SQL(op.Context(), auth.Catalog, node); err != nil {
						return err
					}
					// Validate the original published recipe first. Old callable
					// identities may be copied unchanged only into its same physical
					// TEMP lifetime, not certified through an unpublished destination.
					for _, dependency := range old.Dependencies {
						if dependency.Address.ClassID != ids.ProcedureClassID || dependency.Epoch == auth.Catalog.Epoch() {
							continue
						}
						sourceNamespace := auth.Catalog.MustNamespace(in.Source.Namespace())
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
			if in.Options&analyze.LikeGenerated != 0 {
				if old, ok := in.Source.ColumnMeta.Generations[attribute]; ok {
					if _, inherited := destination.ColumnMeta.Generations[target]; inherited {
						diagnostic := sqlError("2BP01", fmt.Sprintf("cannot drop default value for column %s of table %s because column %s of table %s requires it", destination.Columns[in.TargetSlot(slot)].Name, destination.Name(), destination.Columns[in.TargetSlot(slot)].Name, destination.Name())).(*sqlstate.Error)
						diagnostic.Hint = fmt.Sprintf("You can drop column %s of table %s instead.", destination.Columns[in.TargetSlot(slot)].Name, destination.Name())
						return diagnostic
					}
					node, err := stored.RemapLikeExpression(in, old.Expression, destination, auth.Catalog)
					if err != nil {
						return err
					}
					planned, err := stored.RemapLikeExpression(in, old.Planned, destination, auth.Catalog)
					if err != nil {
						return err
					}
					definition := catalog.ColumnGeneration{Kind: old.Kind, Expression: node, Planned: planned}
					if _, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: destination, Dependencies: &definition.Dependencies, CommentSnapshot: &definition.CatalogSnapshot}).SQL(op.Context(), auth.Catalog, node); err != nil {
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

func (e *Engine) stageLikeChecks(op *session.Operation, auth AuthorizationView, destination *catalog.Relation, inputs []analyze.LikeInput) error {
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
		for _, old := range orderedChecks(in.Source) {
			if names[old.Name] {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, old.Name, destination.Name()))
			}
			if ordinal == int(^uint(0)>>1) {
				return fmt.Errorf("stuntgres: CHECK ordinal exhausted")
			}
			node, err := stored.RemapLikeExpression(in, old.Expression, destination, auth.Catalog)
			if err != nil {
				return err
			}
			definition := catalog.CheckConstraint{Name: old.Name, NoInherit: old.NoInherit, Ordinal: ordinal, Expression: node}
			var dependencies []ids.ObjectRef
			if _, err := (stored.ScalarRenderer{ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: destination, Dependencies: &dependencies, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(op.Context(), auth.Catalog, node); err != nil {
				return err
			}
			for _, ref := range dependencies {
				address := ref.Address
				address.SubID = 0
				revision := destination.Revision()
				if provider, ok := auth.Catalog.Descriptor(address); ok {
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
