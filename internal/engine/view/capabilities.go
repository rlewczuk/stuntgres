package view

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// CapabilityLeaf supplies facts for represented builtin views and foreign tables.
// It must inspect immutable metadata only, without invoking foreign callbacks.
type CapabilityLeaf func(context.Context, catalog.Read, *catalog.Relation, []int32) (uint32, error)

// Capabilities computes structural UPDATE/INSERT/DELETE bits (4/8/16), not
// permission to execute DML. A nil subset means all outputs; an empty nonnil
// subset means none. Recursion is ancestry-local because subsets differ by use.
func Capabilities(ctx context.Context, read catalog.Read, relation *catalog.Relation, subset []int32, leaf CapabilityLeaf) (uint32, error) {
	return relationCapabilities(ctx, read, relation, subset, leaf, nil)
}

func relationCapabilities(ctx context.Context, read catalog.Read, relation *catalog.Relation, subset []int32, leaf CapabilityLeaf, ancestry []ids.ObjectAddress) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if relation == nil || read.Get(relation.Address()) != relation {
		return 0, ids.ErrCatalogReplaced
	}
	for _, attribute := range subset {
		if attribute <= 0 || !relation.HasAttribute(attribute) {
			return 0, ids.ErrCatalogReplaced
		}
	}
	switch relation.RelationKind {
	case catalog.TableRelation, catalog.PartitionedRelation:
		return 28, nil
	case catalog.CatalogRelation, catalog.ForeignRelation:
		return leaf(ctx, read, relation, subset)
	case catalog.ViewRelation:
	default:
		return 0, nil
	}
	var rules uint32
	for _, rule := range stored.RulesOf(relation) {
		if !rule.Instead() || rule.Condition() != nil {
			continue
		}
		switch rule.Event() {
		case "UPDATE":
			rules |= 4
		case "INSERT":
			rules |= 8
		case "DELETE":
			rules |= 16
		}
	}
	for _, address := range ancestry {
		if address == relation.Address() {
			return rules, nil
		}
	}
	if relation.View == nil {
		return 0, ids.ErrCatalogReplaced
	}
	project, sourceNode, reason := stored.ViewUpdatabilityShape(relation)
	if reason != "" {
		return rules, nil
	}
	definition := stored.ViewOf(relation)
	if sourceNode.Occurrence < 0 || sourceNode.Occurrence >= len(definition.Sources()) {
		return 0, ids.ErrCatalogReplaced
	}
	sourceLeaf := definition.Sources()[sourceNode.Occurrence]
	if _, err := stored.ViewUseReference(read, relation, sourceLeaf.Ref); err != nil {
		return 0, err
	}
	source, ok := read.Relation(sourceLeaf.Ref.Address)
	if !ok || len(project.Expressions) != len(relation.Attributes) {
		return 0, ids.ErrCatalogReplaced
	}
	mapped := make([]int32, 0, len(project.Expressions))
	for i, expression := range project.Expressions {
		selected := subset == nil
		for _, attribute := range subset {
			if attribute == relation.Attributes[i] {
				selected = true
				break
			}
		}
		if !selected {
			continue
		}
		column, direct := stored.ViewDirectColumn(expression)
		if !direct || !stored.SameViewRelation(column.Origin, sourceLeaf.Ref) {
			continue
		}
		slot, err := StableViewSlot(source, sourceLeaf, column.Slot)
		if err != nil {
			return 0, err
		}
		if slot >= len(source.Attributes) || source.Attributes[slot] <= 0 {
			return 0, ids.ErrCatalogReplaced
		}
		mapped = append(mapped, source.Attributes[slot])
	}
	automatic := uint32(16)
	if len(mapped) != 0 {
		automatic = 28
	}
	base, err := relationCapabilities(ctx, read, source, mapped, leaf, append(ancestry, relation.Address()))
	if err != nil {
		return 0, err
	}
	return rules | (automatic & base), nil
}
