package view

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/stored"
)

// ForeignUpdatableEvents is a metadata-only capability; it must not invoke a
// provider. Keeping the callback here avoids importing the upper FDW feature.
type ForeignUpdatableEvents func(context.Context, catalog.Read, *catalog.Relation) (bool, bool, error)

// ColumnUpdatable is structural UPDATE+DELETE capability, independent of ACLs.
// It never binds a mutation, opens storage, evaluates expressions or uses triggers.
func ColumnUpdatable(ctx context.Context, view catalog.Read, relation *catalog.Relation, attribute int32, foreign ForeignUpdatableEvents) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if relation == nil || view.Get(relation.Address()) != relation || relation.IndexForAttribute(attribute) < 0 {
		return false, ids.ErrCatalogReplaced
	}
	for _, event := range [2]string{"UPDATE", "DELETE"} {
		allowed, err := columnUpdatableEvent(ctx, view, relation, attribute, event, foreign, nil)
		if err != nil || !allowed {
			return false, err
		}
	}
	return true, nil
}

type columnUpdatabilityFrame struct {
	relation ids.ObjectAddress
	parent   *columnUpdatabilityFrame
}

func columnUpdatableEvent(ctx context.Context, view catalog.Read, relation *catalog.Relation, attribute int32, event string, foreign ForeignUpdatableEvents, parent *columnUpdatabilityFrame) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for frame := parent; frame != nil; frame = frame.parent {
		if frame.relation == relation.Address() {
			return false, sqlError("42P17", fmt.Sprintf("infinite recursion detected in rules for relation %q", relation.Name()))
		}
	}
	switch pgcatalog.ClassKind(view, relation) {
	case "r", "p":
		return true, nil
	case "f":
		if foreign == nil {
			return false, fmt.Errorf("stuntgres: missing foreign metadata capability")
		}
		update, remove, err := foreign(ctx, view, relation)
		if event == "UPDATE" {
			return update, err
		}
		return remove, err
	case "v":
	default:
		return false, nil
	}
	for _, rule := range stored.RulesOf(relation) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if rule.Event() == event && rule.Instead() && rule.Condition() == nil {
			return true, nil
		}
	}
	definition := stored.ViewOf(relation)
	if definition == nil {
		return false, nil
	}
	project, sourceNode, reason := stored.ViewUpdatabilityShape(relation)
	if reason != "" {
		return false, nil
	}
	if sourceNode.Occurrence < 0 || sourceNode.Occurrence >= len(definition.Sources()) {
		return false, ids.ErrCatalogReplaced
	}
	leaf := definition.Sources()[sourceNode.Occurrence]
	source, ok := view.Relation(leaf.Ref.Address)
	if !ok {
		return false, ids.ErrCatalogReplaced
	}
	if _, err := stored.ViewUseReference(view, relation, leaf.Ref); err != nil {
		return false, err
	}
	mapped := int32(0)
	if event == "UPDATE" {
		slot := relation.IndexForAttribute(attribute)
		if slot < 0 || slot >= len(project.Expressions) {
			return false, ids.ErrCatalogReplaced
		}
		column, direct := stored.ViewDirectColumn(project.Expressions[slot])
		if !direct || !stored.SameViewRelation(column.Origin, leaf.Ref) {
			return false, nil
		}
		live, err := StableViewSlot(source, leaf, column.Slot)
		if err != nil {
			return false, err
		}
		mapped = source.Attributes[live]
	}
	frame := columnUpdatabilityFrame{relation: relation.Address(), parent: parent}
	return columnUpdatableEvent(ctx, view, source, mapped, event, foreign, &frame)
}
