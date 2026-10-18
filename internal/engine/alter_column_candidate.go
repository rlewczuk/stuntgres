package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Stable attributes own defaults, ACLs, comments, constraints and dependency edges.
func (scope *StatementScope) dropColumnCandidate(stage *CatalogStage, old *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, changes *catalogChanges) (*catalog.Relation, error) {
	return dropColumnCandidate(scope.op.Context(), stage, old, ref, a, scope, changes, nil)
}

func dropColumnCandidate(ctx context.Context, stage *CatalogStage, old *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction, scope *StatementScope, changes *catalogChanges, project func(*catalog.Relation, *catalog.Relation) error) (*catalog.Relation, error) {
	return ordinaryColumnDrop(stageDropProvider(ctx, stage, scope, changes, project), old, ref, a)
}

func ordinaryColumnDrop(provider *dropProvider, old *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction) (*catalog.Relation, error) {
	ctx := provider.ctx
	if a.Mode != parser.DropRestrict && a.Mode != parser.DropCascade {
		return nil, fmt.Errorf("stuntgres: invalid column drop")
	}
	if old.RelationKind != catalog.CompositeRelation && isSystemColumn(a.Column.Text) {
		return nil, sqlError("0A000", fmt.Sprintf(`cannot drop system column "%s"`, a.Column.Text))
	}
	slot, exists := old.Index[a.Column.Text]
	if !exists {
		if !a.IfExists {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, old.Name()))
		}
		if provider.scope != nil {
			provider.missingColumnNotice(a.Column.Text, old.Name())
		}
		return old, nil
	}
	history := relationAttributeMetadata(old, slot)
	history.Name = fmt.Sprintf("........pg.dropped.%d........", history.Number)
	history.TypeID, history.NotNull, history.HasDefault, history.Generated, history.Dropped = 0, false, false, "", true
	target := ref.Address
	target.SubID = old.Attributes[slot]
	for _, attribute := range old.Attributes {
		definition, generated := old.Generations[attribute]
		if !generated || attribute == target.SubID {
			continue
		}
		dependent := false
		for _, dependency := range definition.Dependencies {
			if dependency.Address == target {
				dependent = true
				break
			}
		}
		if !dependent {
			continue
		}
		generatedSlot := old.IndexForAttribute(attribute)
		if a.Mode == parser.DropRestrict {
			message := "cannot drop desired object(s) because other objects depend on them"
			if provider.scope == nil || provider.scope.inheritanceAlter == nil {
				message = fmt.Sprintf("cannot drop column %s of table %s because other objects depend on it", a.Column.Text, old.Name())
			}
			diagnostic := sqlError("2BP01", message).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf("column %s of table %s depends on column %s of table %s", old.Columns[generatedSlot].Name, old.Name(), a.Column.Text, old.Name())
			diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			return nil, diagnostic
		}
		dependentAction := a
		dependentAction.Column = parser.Identifier{Text: old.Columns[generatedSlot].Name}
		remaining, err := ordinaryColumnDrop(provider, old, ref, dependentAction)
		if err != nil {
			return nil, err
		}
		return ordinaryColumnDrop(provider, remaining, ref, a)
	}
	// Dependency passes must see preceding actions in this same ALTER list.
	if err := provider.replace(old); err != nil {
		return nil, err
	}
	if old.RelationKind == catalog.CompositeRelation {
		if err := provider.compositeFieldChecks(old, target.SubID, a.Mode); err != nil {
			return nil, err
		}
	}
	foreign, err := provider.foreignKeyColumnDependencies(target, a.Mode)
	if err != nil {
		return nil, err
	}
	if err := provider.applyViewDependents([]ids.ObjectAddress{target}, a.Mode, foreign...); err != nil {
		return nil, err
	}
	// The dependency pass can prune surviving definitions on this same table.
	// Its staged descriptor, not the pre-cascade maps, owns the remaining shape.
	old = provider.descriptor(ref.Address).(*catalog.Relation)
	next, err := dropColumnMetadata(ctx, provider.base, provider.descriptor, old, target, history)
	if err != nil {
		return nil, err
	}

	// Publish the private shape to dependency passes before removing its AUTO
	// sequences: CHECKs on the dropped column are already gone.
	if err := provider.replace(next); err != nil {
		return nil, err
	}
	if err := provider.ownedExpressionDependencies([]ids.ObjectAddress{target}, a.Mode); err != nil {
		return nil, err
	}
	return provider.descriptor(ref.Address).(*catalog.Relation), nil
}

// dropColumnMetadata cooks the stable-slot migration once. Dependency effects
// supply the actual surviving provider descriptor and index identities.
func dropColumnMetadata(ctx context.Context, view CatalogRead, descriptor func(ids.ObjectAddress) Descriptor, old *catalog.Relation, target ids.ObjectAddress, history catalog.AttributeMetadata) (*catalog.Relation, error) {
	slot := old.IndexForAttribute(target.SubID)
	next := *old
	if old.Foreign != nil {
		foreign := *old.Foreign
		foreign.Columns = maps.Clone(old.Foreign.Columns)
		delete(foreign.Columns, target.SubID)
		next.Foreign = &foreign
	}
	next.DroppedAttributes = maps.Clone(old.DroppedAttributes)
	if next.DroppedAttributes == nil {
		next.DroppedAttributes = make(map[int32]catalog.AttributeMetadata)
	}
	next.DroppedAttributes[history.Number] = history
	next.Columns = make([]ColumnDescriptor, 0, len(old.Columns)-1)
	next.Attributes = make([]int32, 0, len(old.Attributes)-1)
	next.Index = make(map[string]int, len(old.Index)-1)
	for i, column := range old.Columns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i == slot {
			continue
		}
		next.Index[column.Name] = len(next.Columns)
		next.Columns = append(next.Columns, column)
		next.Attributes = append(next.Attributes, old.Attributes[i])
	}
	next.NotNullConstraints = nil
	for _, constraint := range old.NotNullConstraints {
		if constraint.Attribute != old.Attributes[slot] {
			next.NotNullConstraints = append(next.NotNullConstraints, constraint)
		}
	}
	next.UniqueConstraints = nil
	for _, constraint := range old.UniqueConstraints {
		dependent := false
		for _, attribute := range constraint.Attributes {
			if attribute == old.Attributes[slot] {
				dependent = true
				break
			}
		}
		index, ok := descriptor(constraint.Index).(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: missing dropped-column unique index")
		}
		dependent = dependent || slices.Contains(index.SQLIndex.Include, old.Attributes[slot])
		if !dependent {
			next.UniqueConstraints = append(next.UniqueConstraints, constraint)
		}
	}
	next.ExclusionConstraints = nil
	for _, constraint := range old.ExclusionConstraints {
		index, ok := descriptor(constraint.Index).(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: missing dropped-column exclusion index")
		}
		if !uniqueIndexTypeAffected(index.SQLIndex, []columnTransformation{{attribute: old.Attributes[slot]}}) {
			next.ExclusionConstraints = append(next.ExclusionConstraints, constraint)
		}
	}
	next.ForeignKeys = nil
	for _, f := range old.ForeignKeys {
		if !slices.Contains(f.Attributes, old.Attributes[slot]) {
			next.ForeignKeys = append(next.ForeignKeys, f)
		}
	}
	next.CheckConstraints = nil
	if len(old.CheckConstraints) != 0 {
		slots := make(map[int32]int, len(next.Attributes))
		for i, attr := range next.Attributes {
			slots[attr] = i
		}
		for _, c := range old.CheckConstraints {
			dependent := false
			for _, dep := range c.Dependencies {
				if dep.Ref.Address == target {
					dependent = true
					break
				}
			}
			if dependent {
				continue
			}
			expression, err := remapBoundExpression(c.Expression, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			c.Expression = expression
			next.CheckConstraints = append(next.CheckConstraints, c)
		}
	}
	if _, exists := old.Defaults[old.Attributes[slot]]; exists {
		next.Defaults = nil
		if len(old.Defaults) > 1 {
			next.Defaults = make(map[int32]catalog.ColumnDefault, len(old.Defaults)-1)
			for attribute, definition := range old.Defaults {
				if attribute != old.Attributes[slot] {
					next.Defaults[attribute] = definition
				}
			}
		}
	}
	next.Generations = nil
	if len(old.Generations) != 0 {
		slots := make(map[int32]int, len(next.Attributes))
		for i, attribute := range next.Attributes {
			slots[attribute] = i
		}
		next.Generations = make(map[int32]catalog.ColumnGeneration, len(old.Generations))
		for attribute, definition := range old.Generations {
			if attribute == target.SubID {
				continue
			}
			planned, err := remapBoundExpression(definition.Planned, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			definition.Planned = planned
			node, err := remapBoundExpression(definition.Expression, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			definition.Expression = node
			next.Generations[attribute] = definition
		}
	}
	if err := refreshGenerationChecks(&next, view); err != nil {
		return nil, err
	}
	if _, exists := old.Identities[old.Attributes[slot]]; exists {
		next.Identities = make(map[int32]catalog.ColumnIdentity, len(old.Identities)-1)
		for attribute, identity := range old.Identities {
			if attribute != old.Attributes[slot] {
				next.Identities[attribute] = identity
			}
		}
	}
	if _, exists := old.ColumnACL[old.Attributes[slot]]; exists {
		next.ColumnACL = nil
		for sub, acl := range old.ColumnACL {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if sub == old.Attributes[slot] {
				continue
			}
			if next.ColumnACL == nil {
				next.ColumnACL = make(map[int32]authorization.ACLValue, len(old.ColumnACL)-1)
			}
			next.ColumnACL[sub] = acl
		}
	}
	if _, exists := old.ColumnComments[old.Attributes[slot]]; exists {
		next.ColumnComments = nil
		for sub, text := range old.ColumnComments {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if sub == old.Attributes[slot] {
				continue
			}
			if next.ColumnComments == nil {
				next.ColumnComments = make(map[int32]string, len(old.ColumnComments)-1)
			}
			next.ColumnComments[sub] = text
		}
	}
	transitionPhysicalNotNull(old, &next)
	return &next, nil
}
