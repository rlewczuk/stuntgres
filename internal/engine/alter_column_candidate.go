package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Stable attributes own defaults, ACLs, comments, constraints and dependency edges.
func (scope *StatementScope) dropColumnCandidate(stage *CatalogStage, old *relationDescriptor, ref ObjectRef, a parser.AlterTableAction, changes *catalogChanges) (*relationDescriptor, error) {
	return dropColumnCandidate(scope.op.Context(), stage, old, ref, a, scope, changes, nil)
}

func dropColumnCandidate(ctx context.Context, stage *CatalogStage, old *relationDescriptor, ref ObjectRef, a parser.AlterTableAction, scope *StatementScope, changes *catalogChanges, project func(*relationDescriptor, *relationDescriptor) error) (*relationDescriptor, error) {
	return ordinaryColumnDrop(stageDropProvider(ctx, stage, scope, changes, project), old, ref, a)
}

func ordinaryColumnDrop(provider *dropProvider, old *relationDescriptor, ref ObjectRef, a parser.AlterTableAction) (*relationDescriptor, error) {
	ctx := provider.ctx
	if a.Mode != parser.DropRestrict && a.Mode != parser.DropCascade {
		return nil, fmt.Errorf("stuntgres: invalid column drop")
	}
	if old.relationKind != compositeRelation && isSystemColumn(a.Column.Text) {
		return nil, sqlError("0A000", fmt.Sprintf(`cannot drop system column "%s"`, a.Column.Text))
	}
	slot, exists := old.index[a.Column.Text]
	if !exists {
		if !a.IfExists {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, old.name))
		}
		if provider.scope != nil {
			provider.missingColumnNotice(a.Column.Text, old.name)
		}
		return old, nil
	}
	history := relationAttributeMetadata(old, slot)
	history.name = fmt.Sprintf("........pg.dropped.%d........", history.number)
	history.typeID, history.notNull, history.hasDefault, history.generated, history.dropped = 0, false, false, "", true
	target := ref.Address
	target.SubID = old.attributes[slot]
	for _, attribute := range old.attributes {
		definition, generated := old.generations[attribute]
		if !generated || attribute == target.SubID {
			continue
		}
		dependent := false
		for _, dependency := range definition.dependencies {
			if dependency.Address == target {
				dependent = true
				break
			}
		}
		if !dependent {
			continue
		}
		generatedSlot := old.indexForAttribute(attribute)
		if a.Mode == parser.DropRestrict {
			message := "cannot drop desired object(s) because other objects depend on them"
			if provider.scope == nil || provider.scope.inheritanceAlter == nil {
				message = fmt.Sprintf("cannot drop column %s of table %s because other objects depend on it", a.Column.Text, old.name)
			}
			diagnostic := sqlError("2BP01", message).(*sqlstate.Error)
			diagnostic.Detail = fmt.Sprintf("column %s of table %s depends on column %s of table %s", old.columns[generatedSlot].Name, old.name, a.Column.Text, old.name)
			diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			return nil, diagnostic
		}
		dependentAction := a
		dependentAction.Column = parser.Identifier{Text: old.columns[generatedSlot].Name}
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
	if old.relationKind == compositeRelation {
		if err := provider.compositeFieldChecks(old, target.SubID, a.Mode); err != nil {
			return nil, err
		}
	}
	foreign, err := provider.foreignKeyColumnDependencies(target, a.Mode)
	if err != nil {
		return nil, err
	}
	if err := provider.applyViewDependents([]ObjectAddress{target}, a.Mode, foreign...); err != nil {
		return nil, err
	}
	// The dependency pass can prune surviving definitions on this same table.
	// Its staged descriptor, not the pre-cascade maps, owns the remaining shape.
	old = provider.descriptor(ref.Address).(*relationDescriptor)
	next, err := dropColumnMetadata(ctx, provider.base, provider.descriptor, old, target, history)
	if err != nil {
		return nil, err
	}

	// Publish the private shape to dependency passes before removing its AUTO
	// sequences: CHECKs on the dropped column are already gone.
	if err := provider.replace(next); err != nil {
		return nil, err
	}
	if err := provider.ownedExpressionDependencies([]ObjectAddress{target}, a.Mode); err != nil {
		return nil, err
	}
	return provider.descriptor(ref.Address).(*relationDescriptor), nil
}

// dropColumnMetadata cooks the stable-slot migration once. Dependency effects
// supply the actual surviving provider descriptor and index identities.
func dropColumnMetadata(ctx context.Context, catalog CatalogRead, descriptor func(ObjectAddress) Descriptor, old *relationDescriptor, target ObjectAddress, history attributeMetadata) (*relationDescriptor, error) {
	slot := old.indexForAttribute(target.SubID)
	next := *old
	if old.foreign != nil {
		foreign := *old.foreign
		foreign.columns = maps.Clone(old.foreign.columns)
		delete(foreign.columns, target.SubID)
		next.foreign = &foreign
	}
	next.droppedAttributes = maps.Clone(old.droppedAttributes)
	if next.droppedAttributes == nil {
		next.droppedAttributes = make(map[int32]attributeMetadata)
	}
	next.droppedAttributes[history.number] = history
	next.columns = make([]ColumnDescriptor, 0, len(old.columns)-1)
	next.attributes = make([]int32, 0, len(old.attributes)-1)
	next.index = make(map[string]int, len(old.index)-1)
	for i, column := range old.columns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i == slot {
			continue
		}
		next.index[column.Name] = len(next.columns)
		next.columns = append(next.columns, column)
		next.attributes = append(next.attributes, old.attributes[i])
	}
	next.notNullConstraints = nil
	for _, constraint := range old.notNullConstraints {
		if constraint.Attribute != old.attributes[slot] {
			next.notNullConstraints = append(next.notNullConstraints, constraint)
		}
	}
	next.uniqueConstraints = nil
	for _, constraint := range old.uniqueConstraints {
		dependent := false
		for _, attribute := range constraint.Attributes {
			if attribute == old.attributes[slot] {
				dependent = true
				break
			}
		}
		index, ok := descriptor(constraint.Index).(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			return nil, fmt.Errorf("stuntgres: missing dropped-column unique index")
		}
		dependent = dependent || slices.Contains(index.sqlIndex.include, old.attributes[slot])
		if !dependent {
			next.uniqueConstraints = append(next.uniqueConstraints, constraint)
		}
	}
	next.exclusionConstraints = nil
	for _, constraint := range old.exclusionConstraints {
		index, ok := descriptor(constraint.Index).(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			return nil, fmt.Errorf("stuntgres: missing dropped-column exclusion index")
		}
		if !uniqueIndexTypeAffected(index.sqlIndex, []columnTransformation{{attribute: old.attributes[slot]}}) {
			next.exclusionConstraints = append(next.exclusionConstraints, constraint)
		}
	}
	next.foreignKeys = nil
	for _, f := range old.foreignKeys {
		if !slices.Contains(f.Attributes, old.attributes[slot]) {
			next.foreignKeys = append(next.foreignKeys, f)
		}
	}
	next.checkConstraints = nil
	if len(old.checkConstraints) != 0 {
		slots := make(map[int32]int, len(next.attributes))
		for i, attr := range next.attributes {
			slots[attr] = i
		}
		for _, c := range old.checkConstraints {
			dependent := false
			for _, dep := range c.dependencies {
				if dep.ref.Address == target {
					dependent = true
					break
				}
			}
			if dependent {
				continue
			}
			expression, err := remapBoundExpression(c.expression, func(ref ObjectRef) ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			c.expression = expression
			next.checkConstraints = append(next.checkConstraints, c)
		}
	}
	if _, exists := old.defaults[old.attributes[slot]]; exists {
		next.defaults = nil
		if len(old.defaults) > 1 {
			next.defaults = make(map[int32]columnDefault, len(old.defaults)-1)
			for attribute, definition := range old.defaults {
				if attribute != old.attributes[slot] {
					next.defaults[attribute] = definition
				}
			}
		}
	}
	next.generations = nil
	if len(old.generations) != 0 {
		slots := make(map[int32]int, len(next.attributes))
		for i, attribute := range next.attributes {
			slots[attribute] = i
		}
		next.generations = make(map[int32]columnGeneration, len(old.generations))
		for attribute, definition := range old.generations {
			if attribute == target.SubID {
				continue
			}
			planned, err := remapBoundExpression(definition.planned, func(ref ObjectRef) ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			definition.planned = planned
			node, err := remapBoundExpression(definition.expression, func(ref ObjectRef) ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			definition.expression = node
			next.generations[attribute] = definition
		}
	}
	if err := refreshGenerationChecks(&next, catalog); err != nil {
		return nil, err
	}
	if _, exists := old.identities[old.attributes[slot]]; exists {
		next.identities = make(map[int32]columnIdentity, len(old.identities)-1)
		for attribute, identity := range old.identities {
			if attribute != old.attributes[slot] {
				next.identities[attribute] = identity
			}
		}
	}
	if _, exists := old.columnACL[old.attributes[slot]]; exists {
		next.columnACL = nil
		for sub, acl := range old.columnACL {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if sub == old.attributes[slot] {
				continue
			}
			if next.columnACL == nil {
				next.columnACL = make(map[int32]authorization.ACLValue, len(old.columnACL)-1)
			}
			next.columnACL[sub] = acl
		}
	}
	if _, exists := old.columnComments[old.attributes[slot]]; exists {
		next.columnComments = nil
		for sub, text := range old.columnComments {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if sub == old.attributes[slot] {
				continue
			}
			if next.columnComments == nil {
				next.columnComments = make(map[int32]string, len(old.columnComments)-1)
			}
			next.columnComments[sub] = text
		}
	}
	transitionPhysicalNotNull(old, &next)
	return &next, nil
}
