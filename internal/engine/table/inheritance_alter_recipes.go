package table

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// Column locality describes declaration provenance, not ownership of a default
// or generation recipe. ONLY changes to a provider can leave a valid descendant
// recipe that must be replayed without making its attribute local.
func RetainInheritedRecipes(view catalog.Read, child *catalog.Relation, attribute int32, provenance catalog.InheritedColumn) (catalog.InheritedColumn, error) {
	childDefault, hasDefault := child.ColumnMeta.Defaults[attribute]
	childGeneration, generated := child.ColumnMeta.Generations[attribute]
	suppliedDefault, suppliedGeneration := false, false
	for _, origin := range provenance.Origins {
		parent, ok := view.Relation(origin.Parent)
		if !ok {
			return provenance, ids.ErrCatalogReplaced
		}
		if definition, exists := parent.ColumnMeta.Defaults[origin.Attribute]; exists {
			suppliedDefault = true
			if !hasDefault || !SameInheritedExpression(definition.Expression, childDefault.Expression, child, view) {
				provenance.ExplicitDefault = true
			}
		}
		if definition, exists := parent.ColumnMeta.Generations[origin.Attribute]; exists && generated {
			suppliedGeneration = true
			mapper := analyze.LikeInput{Source: parent, TargetSlots: make([]int, len(parent.Columns))}
			for i, column := range parent.Columns {
				slot, ok := child.Index[column.Name]
				if !ok {
					return provenance, ids.ErrCatalogReplaced
				}
				mapper.TargetSlots[i] = slot
			}
			mapped, err := stored.RemapLikeExpression(&mapper, definition.Expression, child, view)
			if err != nil {
				return provenance, err
			}
			if !SameInheritedExpression(mapped, childGeneration.Expression, child, view) {
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
func (t *DumpRelation) EmitInheritedDefaults(ctx context.Context, catalogView catalog.Read, deferDefaults bool, emit func(string) error) error {
	if len(t.Recipes) == 0 {
		return nil
	}
	prefix := "ALTER TABLE ONLY " + catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name) + " ALTER COLUMN "
	for i, column := range t.Columns {
		attribute := t.Attributes[i]
		provenance := t.Recipes[attribute]
		if provenance.Local {
			continue
		}
		if provenance.ExplicitDefault && !(deferDefaults && t.DeferredDefinitions[attribute]) {
			clause := " DROP DEFAULT"
			if definition, exists := t.Defaults[attribute]; exists {
				text, err := stored.DefaultSQL(ctx, catalogView, definition.Expression)
				if err != nil {
					return err
				}
				clause = " SET DEFAULT " + text
			}
			if err := emit(prefix + catalog.QuoteIdentifier(column.Name) + clause + ";\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

func InheritedDefaultReplay(raw *parser.AlterTableDefault, relation *catalog.Relation) bool {
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

func (t *DumpRelation) PrepareInheritedRecipes(view catalog.Read) error {
	graph := t.Descriptor.Inheritance
	if graph == nil {
		return nil
	}
	for _, attribute := range t.Attributes {
		provenance := graph.Columns[attribute]
		if provenance.Local && len(provenance.Origins) == 0 {
			continue
		}
		effective, err := RetainInheritedRecipes(view, t.Descriptor, attribute, provenance)
		if err != nil {
			return err
		}
		needed := !provenance.Local && (effective.ExplicitDefault || effective.ExplicitGeneration) || provenance.Local && (!provenance.ExplicitDefault && effective.ExplicitDefault || !provenance.ExplicitGeneration && effective.ExplicitGeneration)
		if needed {
			if t.Recipes == nil {
				t.Recipes = make(map[int32]catalog.InheritedColumn)
			}
			t.Recipes[attribute] = effective
		}
	}
	return nil
}
