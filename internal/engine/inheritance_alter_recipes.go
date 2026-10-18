package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Column locality describes declaration provenance, not ownership of a default
// or generation recipe. ONLY changes to a provider can leave a valid descendant
// recipe that must be replayed without making its attribute local.
func retainInheritedRecipes(view catalog.Read, child *catalog.Relation, attribute int32, provenance catalog.InheritedColumn) (catalog.InheritedColumn, error) {
	childDefault, hasDefault := child.Defaults[attribute]
	childGeneration, generated := child.Generations[attribute]
	suppliedDefault, suppliedGeneration := false, false
	for _, origin := range provenance.Origins {
		parent, ok := view.Relation(origin.Parent)
		if !ok {
			return provenance, ids.ErrCatalogReplaced
		}
		if definition, exists := parent.Defaults[origin.Attribute]; exists {
			suppliedDefault = true
			if !hasDefault || !sameInheritedExpression(definition.Expression, childDefault.Expression, child, view) {
				provenance.ExplicitDefault = true
			}
		}
		if definition, exists := parent.Generations[origin.Attribute]; exists && generated {
			suppliedGeneration = true
			mapper := createLikeInput{source: parent, targetSlots: make([]int, len(parent.Columns))}
			for i, column := range parent.Columns {
				slot, ok := child.Index[column.Name]
				if !ok {
					return provenance, ids.ErrCatalogReplaced
				}
				mapper.targetSlots[i] = slot
			}
			mapped, err := mapper.remap(definition.Expression, child, view)
			if err != nil {
				return provenance, err
			}
			if !sameInheritedExpression(mapped, childGeneration.Expression, child, view) {
				provenance.ExplicitGeneration = true
			}
		}
	}
	if hasDefault && !suppliedDefault {
		provenance.ExplicitDefault = true
	}
	if generated && !suppliedGeneration {
		provenance.ExplicitGeneration = true
	}
	return provenance, nil
}

// Inline redeclarations would change attislocal. These are the same supported
// physical ALTER actions used by callers, before children/data observe recipes.
// Captured-reference defaults retain the existing provider-dependent phase.
func (t *dumpRelation) emitInheritedDefaults(ctx context.Context, catalog catalog.Read, deferDefaults bool, emit func(string) error) error {
	if len(t.recipes) == 0 {
		return nil
	}
	prefix := "ALTER TABLE ONLY " + quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name) + " ALTER COLUMN "
	for i, column := range t.columns {
		attribute := t.attributes[i]
		provenance := t.recipes[attribute]
		if provenance.Local {
			continue
		}
		if provenance.ExplicitDefault && !(deferDefaults && t.deferredDefinitions[attribute]) {
			clause := " DROP DEFAULT"
			if definition, exists := t.defaults[attribute]; exists {
				text, err := defaultSQL(ctx, catalog, definition.Expression)
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

func inheritedDefaultReplay(raw *parser.AlterTableDefault, relation *catalog.Relation) bool {
	if !raw.Only || len(raw.Name.Parts) != 2 || relation.Inheritance == nil {
		return false
	}
	slot, exists := relation.Index[raw.Column.Text]
	if !exists {
		return false
	}
	provenance := relation.Inheritance.Columns[relation.Attributes[slot]]
	return !provenance.Local && len(provenance.Origins) != 0 && provenance.ExplicitDefault
}

func (t *dumpRelation) prepareInheritedRecipes(view catalog.Read) error {
	graph := t.descriptor.Inheritance
	if graph == nil {
		return nil
	}
	for _, attribute := range t.attributes {
		provenance := graph.Columns[attribute]
		if provenance.Local && len(provenance.Origins) == 0 {
			continue
		}
		effective, err := retainInheritedRecipes(view, t.descriptor, attribute, provenance)
		if err != nil {
			return err
		}
		needed := !provenance.Local && (effective.ExplicitDefault || effective.ExplicitGeneration) || provenance.Local && (!provenance.ExplicitDefault && effective.ExplicitDefault || !provenance.ExplicitGeneration && effective.ExplicitGeneration)
		if needed {
			if t.recipes == nil {
				t.recipes = make(map[int32]catalog.InheritedColumn)
			}
			t.recipes[attribute] = effective
		}
	}
	return nil
}
