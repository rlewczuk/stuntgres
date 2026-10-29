package table

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func OrdinaryColumnDrop(provider DropDependencyReader, old *catalog.Relation, ref ids.ObjectRef, a parser.AlterTableAction) (*catalog.Relation, error) {
	ctx := provider.Context()
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
		if provider.StatementBound() {
			provider.Notice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`column "%s" of relation "%s" does not exist, skipping`, a.Column.Text, old.Name())})
		}
		return old, nil
	}
	target := ref.Address
	target.SubID = old.Attributes[slot]
	// Dependency passes must see preceding actions in this same ALTER list.
	if err := provider.Replace(old); err != nil {
		return nil, err
	}
	if err := provider.ColumnDependencies(target, a.Mode); err != nil {
		return nil, err
	}
	// The dependency pass can prune surviving definitions on this same table.
	// Its staged descriptor, not the pre-cascade maps, owns the remaining shape.
	old = provider.Descriptor(ref.Address).(*catalog.Relation)
	next, err := dropColumnsMetadata(ctx, provider.BaseRead(), provider.Descriptor, old, []int32{target.SubID})
	if err != nil {
		return nil, err
	}

	// Stage the cooked shape before consuming its already-preflighted AUTO
	// sequence payload. No dependency controller is re-entered here.
	if err := provider.Replace(next); err != nil {
		return nil, err
	}
	if err := RemoveColumnSequences(provider, []ids.ObjectAddress{target}); err != nil {
		return nil, err
	}
	return provider.Descriptor(ref.Address).(*catalog.Relation), nil
}

// dropColumnsMetadata cooks one final stable-slot migration. All captured
// generated attributes are removed before any surviving recipe is remapped.
// Index descriptors must remain readable until their owner metadata is pruned.
func dropColumnsMetadata(ctx context.Context, view catalog.Read, descriptor func(ids.ObjectAddress) catalog.Descriptor, old *catalog.Relation, attributes []int32) (*catalog.Relation, error) {
	dropped := func(attribute int32) bool { return slices.Contains(attributes, attribute) }
	next := *old
	next.ColumnMeta.Dropped = maps.Clone(old.ColumnMeta.Dropped)
	if next.ColumnMeta.Dropped == nil {
		next.ColumnMeta.Dropped = make(map[int32]catalog.AttributeMetadata, len(attributes))
	}
	for _, attribute := range attributes {
		slot := old.IndexForAttribute(attribute)
		if slot < 0 {
			return nil, fmt.Errorf("stuntgres: missing captured drop attribute")
		}
		history := droppedAttributeMetadata(old, slot)
		next.ColumnMeta.Dropped[history.Number] = history
	}
	if old.Foreign != nil {
		foreign := *old.Foreign
		foreign.Columns = maps.Clone(old.Foreign.Columns)
		for _, attribute := range attributes {
			delete(foreign.Columns, attribute)
		}
		next.Foreign = &foreign
	}
	next.Columns = make([]catalog.Column, 0, len(old.Columns)-len(attributes))
	next.Attributes = make([]int32, 0, len(old.Attributes)-len(attributes))
	next.Index = make(map[string]int, len(old.Index)-len(attributes))
	for i, column := range old.Columns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if dropped(old.Attributes[i]) {
			continue
		}
		next.Index[column.Name] = len(next.Columns)
		next.Columns = append(next.Columns, column)
		next.Attributes = append(next.Attributes, old.Attributes[i])
	}
	next.Constraints.NotNull = nil
	for _, constraint := range old.Constraints.NotNull {
		if !dropped(constraint.Attribute) {
			next.Constraints.NotNull = append(next.Constraints.NotNull, constraint)
		}
	}
	next.Constraints.Unique = nil
	for _, constraint := range old.Constraints.Unique {
		dependent := slices.ContainsFunc(constraint.Attributes, dropped)
		idx, ok := descriptor(constraint.Index).(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: missing dropped-column unique index")
		}
		dependent = dependent || slices.ContainsFunc(idx.SQLIndex.Include, dropped)
		if !dependent {
			next.Constraints.Unique = append(next.Constraints.Unique, constraint)
		}
	}
	var transformations []index.ColumnTransformation
	if len(old.Constraints.Exclusion) != 0 {
		transformations = make([]index.ColumnTransformation, len(attributes))
		for i, attribute := range attributes {
			transformations[i].Attribute = attribute
		}
	}
	next.Constraints.Exclusion = nil
	for _, constraint := range old.Constraints.Exclusion {
		idx, ok := descriptor(constraint.Index).(*catalog.Relation)
		if !ok || idx.SQLIndex == nil {
			return nil, fmt.Errorf("stuntgres: missing dropped-column exclusion index")
		}
		if !index.UniqueIndexTypeAffected(idx.SQLIndex, transformations) {
			next.Constraints.Exclusion = append(next.Constraints.Exclusion, constraint)
		}
	}
	next.Constraints.ForeignKeys = nil
	for _, f := range old.Constraints.ForeignKeys {
		if !slices.ContainsFunc(f.Attributes, dropped) {
			next.Constraints.ForeignKeys = append(next.Constraints.ForeignKeys, f)
		}
	}
	var slots map[int32]int
	if len(old.Constraints.Check) != 0 || len(old.ColumnMeta.Generations) != 0 {
		slots = make(map[int32]int, len(next.Attributes))
		for i, attribute := range next.Attributes {
			slots[attribute] = i
		}
	}
	next.Constraints.Check = nil
	for _, c := range old.Constraints.Check {
		if slices.ContainsFunc(c.Dependencies, func(dep catalog.ExpressionDependency) bool {
			return dep.Ref.Address.ClassID == old.Address().ClassID && dep.Ref.Address.ObjectID == old.Address().ObjectID && dropped(dep.Ref.Address.SubID)
		}) {
			continue
		}
		expression, err := stored.RemapExpression(c.Expression, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, slots)
		if err != nil {
			return nil, err
		}
		c.Expression = expression
		next.Constraints.Check = append(next.Constraints.Check, c)
	}
	if slices.ContainsFunc(attributes, func(attribute int32) bool {
		_, exists := old.ColumnMeta.Defaults[attribute]
		return exists
	}) {
		next.ColumnMeta.Defaults = maps.Clone(old.ColumnMeta.Defaults)
		for _, attribute := range attributes {
			delete(next.ColumnMeta.Defaults, attribute)
		}
	}
	next.ColumnMeta.Generations = nil
	if len(old.ColumnMeta.Generations) != 0 {
		next.ColumnMeta.Generations = make(map[int32]catalog.ColumnGeneration, len(old.ColumnMeta.Generations))
		for attribute, definition := range old.ColumnMeta.Generations {
			if dropped(attribute) {
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
	if slices.ContainsFunc(attributes, func(attribute int32) bool {
		_, exists := old.ColumnMeta.Identities[attribute]
		return exists
	}) {
		next.ColumnMeta.Identities = maps.Clone(old.ColumnMeta.Identities)
		for _, attribute := range attributes {
			delete(next.ColumnMeta.Identities, attribute)
		}
	}
	if slices.ContainsFunc(attributes, func(attribute int32) bool {
		_, exists := old.Security.ColumnACL[attribute]
		return exists
	}) {
		next.Security.ColumnACL = maps.Clone(old.Security.ColumnACL)
		for _, attribute := range attributes {
			delete(next.Security.ColumnACL, attribute)
		}
	}
	if slices.ContainsFunc(attributes, func(attribute int32) bool {
		_, exists := old.ColumnMeta.Comments[attribute]
		return exists
	}) {
		next.ColumnMeta.Comments = maps.Clone(old.ColumnMeta.Comments)
		for _, attribute := range attributes {
			delete(next.ColumnMeta.Comments, attribute)
		}
	}
	if len(old.ColumnMeta.Storage) != 0 {
		next.ColumnMeta.Storage = maps.Clone(old.ColumnMeta.Storage)
		for _, attribute := range attributes {
			delete(next.ColumnMeta.Storage, attribute)
		}
	}
	if len(old.ColumnMeta.Statistics) != 0 {
		next.ColumnMeta.Statistics = maps.Clone(old.ColumnMeta.Statistics)
		for _, attribute := range attributes {
			delete(next.ColumnMeta.Statistics, attribute)
		}
	}
	TransitionPhysicalNotNull(old, &next)
	return &next, nil
}

// Recursive inheritance preparation batches removed origins. PostgreSQL deletes
// a nonlocal column on the final origin's visit, when its pre-drop count is one,
// rather than exposing the earlier batched descriptor's original origin count.
func droppedAttributeMetadata(old *catalog.Relation, slot int) catalog.AttributeMetadata {
	history := pgcatalog.AttributeMetadata(old, slot)
	if !history.Local {
		history.Inheritance = 1
	}
	history.Name = fmt.Sprintf("........pg.dropped.%d........", history.Number)
	history.TypeID, history.NotNull, history.HasDefault, history.Generated, history.Dropped = 0, false, false, "", true
	return history
}
