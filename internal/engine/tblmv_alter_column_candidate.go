package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ordinaryColumnDrop(provider dropDependencyReader, old *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction) (*catalog.Relation, error) {
	ctx := provider.context()
	if a.Mode != parser.DropRestrict && a.Mode != parser.DropCascade {
		return nil, fmt.Errorf("stuntgres: invalid column drop")
	}
	if old.RelationKind != catalog.CompositeRelation && catalog.IsSystemColumn(a.Column.Text) {
		return nil, sqlError("0A000", fmt.Sprintf(`cannot drop system column "%s"`, a.Column.Text))
	}
	slot, exists := old.Index[a.Column.Text]
	if !exists {
		if !a.IfExists {
			return nil, sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, a.Column.Text, old.Name()))
		}
		if provider.statementBound() {
			provider.missingColumnNotice(a.Column.Text, old.Name())
		}
		return old, nil
	}
	history := pgcatalog.AttributeMetadata(old, slot)
	history.Name = fmt.Sprintf("........pg.dropped.%d........", history.Number)
	history.TypeID, history.NotNull, history.HasDefault, history.Generated, history.Dropped = 0, false, false, "", true
	target := ref.Address
	target.SubID = old.Attributes[slot]
	for _, attribute := range old.Attributes {
		definition, generated := old.ColumnMeta.Generations[attribute]
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
			if !provider.recursingInheritance() {
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
	if err := provider.Replace(old); err != nil {
		return nil, err
	}
	if old.RelationKind == catalog.CompositeRelation {
		if err := provider.compositeFieldDependencies(old, target.SubID, a.Mode); err != nil {
			return nil, err
		}
	}
	foreign, err := foreignKeyColumnDependencies(provider, target, a.Mode)
	if err != nil {
		return nil, err
	}
	if err := provider.applyViewDependents([]ids.ObjectAddress{target}, a.Mode, foreign...); err != nil {
		return nil, err
	}
	// The dependency pass can prune surviving definitions on this same table.
	// Its staged descriptor, not the pre-cascade maps, owns the remaining shape.
	old = provider.descriptor(ref.Address).(*catalog.Relation)
	next, err := dropColumnMetadata(ctx, provider.baseRead(), provider.descriptor, old, target, history)
	if err != nil {
		return nil, err
	}

	// Publish the private shape to dependency passes before removing its AUTO
	// sequences: CHECKs on the dropped column are already gone.
	if err := provider.Replace(next); err != nil {
		return nil, err
	}
	if err := provider.ownedExpressionDependencies([]ids.ObjectAddress{target}, a.Mode); err != nil {
		return nil, err
	}
	return provider.descriptor(ref.Address).(*catalog.Relation), nil
}

// dropColumnMetadata cooks the stable-slot migration once. Dependency effects
// supply the actual surviving provider descriptor and index identities.
func dropColumnMetadata(ctx context.Context, view catalog.Read, descriptor func(ids.ObjectAddress) catalog.Descriptor, old *catalog.Relation, target ids.ObjectAddress, history catalog.AttributeMetadata) (*catalog.Relation, error) {
	slot := old.IndexForAttribute(target.SubID)
	next := *old
	if old.Foreign != nil {
		foreign := *old.Foreign
		foreign.Columns = maps.Clone(old.Foreign.Columns)
		delete(foreign.Columns, target.SubID)
		next.Foreign = &foreign
	}
	next.ColumnMeta.Dropped = maps.Clone(old.ColumnMeta.Dropped)
	if next.ColumnMeta.Dropped == nil {
		next.ColumnMeta.Dropped = make(map[int32]catalog.AttributeMetadata)
	}
	next.ColumnMeta.Dropped[history.Number] = history
	next.Columns = make([]catalog.Column, 0, len(old.Columns)-1)
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
	next.Constraints.NotNull = nil
	for _, constraint := range old.Constraints.NotNull {
		if constraint.Attribute != old.Attributes[slot] {
			next.Constraints.NotNull = append(next.Constraints.NotNull, constraint)
		}
	}
	next.Constraints.Unique = nil
	for _, constraint := range old.Constraints.Unique {
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
			next.Constraints.Unique = append(next.Constraints.Unique, constraint)
		}
	}
	next.Constraints.Exclusion = nil
	for _, constraint := range old.Constraints.Exclusion {
		idx, ok := descriptor(constraint.Index).(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: missing dropped-column exclusion index")
		}
		if !index.UniqueIndexTypeAffected(idx.SQLIndex, []index.ColumnTransformation{{Attribute: old.Attributes[slot]}}) {
			next.Constraints.Exclusion = append(next.Constraints.Exclusion, constraint)
		}
	}
	next.Constraints.ForeignKeys = nil
	for _, f := range old.Constraints.ForeignKeys {
		if !slices.Contains(f.Attributes, old.Attributes[slot]) {
			next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, f)
		}
	}
	next.Constraints.Check = nil
	if len(old.Constraints.Check) != 0 {
		slots := make(map[int32]int, len(next.Attributes))
		for i, attr := range next.Attributes {
			slots[attr] = i
		}
		for _, c := range old.Constraints.Check {
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
			expression, err := stored.RemapExpression(c.Expression, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			c.Expression = expression
			next.Constraints.Check = append(next.Constraints.Check, c)
		}
	}
	if _, exists := old.ColumnMeta.Defaults[old.Attributes[slot]]; exists {
		next.ColumnMeta.Defaults = nil
		if len(old.ColumnMeta.Defaults) > 1 {
			next.ColumnMeta.Defaults = make(map[int32]catalog.ColumnDefault, len(old.ColumnMeta.Defaults)-1)
			for attribute, definition := range old.ColumnMeta.Defaults {
				if attribute != old.Attributes[slot] {
					next.ColumnMeta.Defaults[attribute] = definition
				}
			}
		}
	}
	next.ColumnMeta.Generations = nil
	if len(old.ColumnMeta.Generations) != 0 {
		slots := make(map[int32]int, len(next.Attributes))
		for i, attribute := range next.Attributes {
			slots[attribute] = i
		}
		next.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration, len(old.ColumnMeta.Generations))
		for attribute, definition := range old.ColumnMeta.Generations {
			if attribute == target.SubID {
				continue
			}
			planned, err := stored.RemapExpression(definition.Planned, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			definition.Planned = planned
			node, err := stored.RemapExpression(definition.Expression, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots)
			if err != nil {
				return nil, err
			}
			definition.Expression = node
			next.ColumnMeta.Generations[attribute] = definition
		}
	}
	if err := stored.RefreshGenerationChecks(&next, view); err != nil {
		return nil, err
	}
	if _, exists := old.ColumnMeta.Identities[old.Attributes[slot]]; exists {
		next.ColumnMeta.Identities = make(map[int32]catalog.ColumnIdentity, len(old.ColumnMeta.Identities)-1)
		for attribute, identity := range old.ColumnMeta.Identities {
			if attribute != old.Attributes[slot] {
				next.ColumnMeta.Identities[attribute] = identity
			}
		}
	}
	if _, exists := old.Security.ColumnACL[old.Attributes[slot]]; exists {
		next.Security.ColumnACL = nil
		for sub, acl := range old.Security.ColumnACL {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if sub == old.Attributes[slot] {
				continue
			}
			if next.Security.ColumnACL == nil {
				next.Security.ColumnACL = make(map[int32]authorization.ACLValue, len(old.Security.ColumnACL)-1)
			}
			next.Security.ColumnACL[sub] = acl
		}
	}
	if _, exists := old.ColumnMeta.Comments[old.Attributes[slot]]; exists {
		next.ColumnMeta.Comments = nil
		for sub, text := range old.ColumnMeta.Comments {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if sub == old.Attributes[slot] {
				continue
			}
			if next.ColumnMeta.Comments == nil {
				next.ColumnMeta.Comments = make(map[int32]string, len(old.ColumnMeta.Comments)-1)
			}
			next.ColumnMeta.Comments[sub] = text
		}
	}
	transitionPhysicalNotNull(old, &next)
	return &next, nil
}
