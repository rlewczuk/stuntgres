package engine

import (
	"context"
	"stuntgres/internal/parser"
)

// Column locality describes declaration provenance, not ownership of a default
// or generation recipe. ONLY changes to a provider can leave a valid descendant
// recipe that must be replayed without making its attribute local.
func retainInheritedRecipes(view CatalogRead, child *relationDescriptor, attribute int32, provenance inheritedColumn) (inheritedColumn, error) {
	childDefault, hasDefault := child.defaults[attribute]
	childGeneration, generated := child.generations[attribute]
	suppliedDefault, suppliedGeneration := false, false
	for _, origin := range provenance.origins {
		parent, ok := view.Relation(origin.parent)
		if !ok {
			return provenance, ErrCatalogReplaced
		}
		if definition, exists := parent.defaults[origin.attribute]; exists {
			suppliedDefault = true
			if !hasDefault || !sameInheritedExpression(definition.expression, childDefault.expression, child, view) {
				provenance.explicitDefault = true
			}
		}
		if definition, exists := parent.generations[origin.attribute]; exists && generated {
			suppliedGeneration = true
			mapper := createLikeInput{source: parent, targetSlots: make([]int, len(parent.columns))}
			for i, column := range parent.columns {
				slot, ok := child.index[column.Name]
				if !ok {
					return provenance, ErrCatalogReplaced
				}
				mapper.targetSlots[i] = slot
			}
			mapped, err := mapper.remap(definition.expression, child, view)
			if err != nil {
				return provenance, err
			}
			if !sameInheritedExpression(mapped, childGeneration.expression, child, view) {
				provenance.explicitGeneration = true
			}
		}
	}
	if hasDefault && !suppliedDefault {
		provenance.explicitDefault = true
	}
	if generated && !suppliedGeneration {
		provenance.explicitGeneration = true
	}
	return provenance, nil
}

// Inline redeclarations would change attislocal. These are the same supported
// physical ALTER actions used by callers, before children/data observe recipes.
// Captured-reference defaults retain the existing provider-dependent phase.
func (t *dumpRelation) emitInheritedDefaults(ctx context.Context, catalog CatalogRead, deferDefaults bool, emit func(string) error) error {
	if len(t.recipes) == 0 {
		return nil
	}
	prefix := "ALTER TABLE ONLY " + quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name) + " ALTER COLUMN "
	for i, column := range t.columns {
		attribute := t.attributes[i]
		provenance := t.recipes[attribute]
		if provenance.local {
			continue
		}
		if provenance.explicitDefault && !(deferDefaults && t.deferredDefinitions[attribute]) {
			clause := " DROP DEFAULT"
			if definition, exists := t.defaults[attribute]; exists {
				text, err := defaultSQL(ctx, catalog, definition.expression)
				if err != nil {
					return err
				}
				clause = " SET DEFAULT " + text
			}
			if err := emit(prefix + quoteIdentifier(column.Name) + clause + ";\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

func inheritedDefaultReplay(raw *parser.AlterTableDefault, relation *relationDescriptor) bool {
	if !raw.Only || len(raw.Name.Parts) != 2 || relation.inheritance == nil {
		return false
	}
	slot, exists := relation.index[raw.Column.Text]
	if !exists {
		return false
	}
	provenance := relation.inheritance.columns[relation.attributes[slot]]
	return !provenance.local && len(provenance.origins) != 0 && provenance.explicitDefault
}

func (t *dumpRelation) prepareInheritedRecipes(view CatalogRead) error {
	graph := t.descriptor.inheritance
	if graph == nil {
		return nil
	}
	for _, attribute := range t.attributes {
		provenance := graph.columns[attribute]
		if provenance.local && len(provenance.origins) == 0 {
			continue
		}
		effective, err := retainInheritedRecipes(view, t.descriptor, attribute, provenance)
		if err != nil {
			return err
		}
		needed := !provenance.local && (effective.explicitDefault || effective.explicitGeneration) || provenance.local && (!provenance.explicitDefault && effective.explicitDefault || !provenance.explicitGeneration && effective.explicitGeneration)
		if needed {
			if t.recipes == nil {
				t.recipes = make(map[int32]inheritedColumn)
			}
			t.recipes[attribute] = effective
		}
	}
	return nil
}
