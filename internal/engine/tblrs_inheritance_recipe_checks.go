package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// RestoreGenerationBoundary rejects a SET EXPRESSION that does not pair a
// captured generated placeholder.
const RestoreGenerationBoundary = "restore accepts only paired generated-placeholder recipe definitions"

// RestoreInheritedCheckBoundary rejects an inherited-recipe CHECK that does
// not pair a restored generation repair.
const RestoreInheritedCheckBoundary = "restore accepts only paired inherited-recipe CHECK definitions"

// CaptureRestoredPlaceholders records, in origins, the generated columns of
// the relation Restore just created as created whose generation a later SET
// EXPRESSION repairs: declared NULL placeholders, and generations every
// parent contributes from an already captured, compatible origin.
func CaptureRestoredPlaceholders(view catalog.Read, created ids.ObjectRef, raw *parser.CreateTable, origins map[table.RestoredGenerationKey]table.RestoredGenerationRecipe) {
	tbl, ok := view.Relation(created.Address)
	if !ok {
		return
	}
	for attribute, definition := range tbl.ColumnMeta.Generations {
		column := tbl.Columns[tbl.IndexForAttribute(attribute)].Name
		declared := false
		for _, element := range raw.Elements {
			if element.Column != nil && element.Column.Name == column {
				for _, constraint := range element.Column.Constraints {
					if constraint.Generation != nil {
						literal, ok := constraint.Generation.Expression.(parser.LiteralExpr)
						declared = ok && literal.Value.Kind == parser.NullValue
					}
				}
			}
		}
		if !declared && tbl.Inheritance != nil {
			inherited := false
			compatible := true
			for _, origin := range tbl.Inheritance.Columns[attribute].Origins {
				parent, ok := view.Relation(origin.Parent)
				if !ok {
					compatible = false
					break
				}
				generation, exists := parent.ColumnMeta.Generations[origin.Attribute]
				if !exists {
					continue
				}
				inherited = true
				record, exists := origins[table.RestoredGenerationKey{Relation: origin.Parent, Attribute: origin.Attribute}]
				if !exists || record.Ref.Domain() != created.Domain() || record.Ref.Epoch != created.Epoch || record.Generation != generation.OID || record.Kind != generation.Kind || record.Usage != parent.Columns[parent.IndexForAttribute(origin.Attribute)].Usage {
					compatible = false
					break
				}
			}
			declared = inherited && compatible && !tbl.Inheritance.Columns[attribute].ExplicitGeneration
		}
		if declared {
			origins[table.RestoredGenerationKey{Relation: created.Address, Attribute: attribute}] = table.RestoredGenerationRecipe{Ref: created, Attribute: attribute, Generation: definition.OID, Usage: tbl.Columns[tbl.IndexForAttribute(attribute)].Usage, Kind: definition.Kind}
		}
	}
}

// RestoredGenerationShape reports whether raw has the one shape a paired
// recipe takes: ALTER TABLE ONLY schema.table ALTER COLUMN ... SET EXPRESSION.
func RestoredGenerationShape(raw *parser.AlterTable) bool {
	return raw.Only && !raw.IfExists && len(raw.Name.Parts) == 2 && len(raw.Actions) == 1 && raw.Actions[0].Action == parser.SetExpression
}

// PairRestoredGeneration accepts the SET EXPRESSION of raw on ref, a
// permanent relation, only when it pairs the placeholder captured for the
// same generation, type and kind; relations validates the relation.
func PairRestoredGeneration(view catalog.Read, relations catalog.Provider, ref ids.ObjectRef, raw *parser.AlterTable, origins map[table.RestoredGenerationKey]table.RestoredGenerationRecipe) error {
	reject := func() error { return sqlError("0A000", RestoreGenerationBoundary) }
	tbl, ok := view.Relation(ref.Address)
	if !ok {
		return reject()
	}
	namespace, namespaceOK := view.Namespace(tbl.Namespace())
	if !namespaceOK || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	slot, exists := tbl.Index[raw.Actions[0].Column.Text]
	if !exists {
		return reject()
	}
	attribute := tbl.Attributes[slot]
	record, exists := origins[table.RestoredGenerationKey{Relation: ref.Address, Attribute: attribute}]
	definition, generated := tbl.ColumnMeta.Generations[attribute]
	if !exists || !generated || record.Ref != ref || record.Attribute != attribute || record.Generation != definition.OID || record.Usage != tbl.Columns[slot].Usage || record.Kind != definition.Kind {
		return reject()
	}
	if relations.Validate(view, tbl) != nil {
		return reject()
	}
	return nil
}

// CaptureRestoredGeneration records the generation the paired SET EXPRESSION
// of raw left on ref.
func CaptureRestoredGeneration(view catalog.Read, ref ids.ObjectRef, raw *parser.AlterTable) (table.RestoredGenerationRecipe, error) {
	tbl, ok := view.Relation(ref.Address)
	if !ok {
		return table.RestoredGenerationRecipe{}, ids.ErrCatalogReplaced
	}
	slot, ok := tbl.Index[raw.Actions[0].Column.Text]
	if !ok {
		return table.RestoredGenerationRecipe{}, ids.ErrCatalogReplaced
	}
	attribute := tbl.Attributes[slot]
	generation := tbl.ColumnMeta.Generations[attribute]
	return table.RestoredGenerationRecipe{Ref: ref, Attribute: attribute, Generation: generation.OID, Usage: tbl.Columns[slot].Usage, Kind: generation.Kind}, nil
}

// RestoredInheritedCheckShape checks the shape of an inherited-recipe CHECK:
// ALTER TABLE schema.table ADD CONSTRAINT name CHECK (...), valid, with a
// restorable scalar expression.
func RestoredInheritedCheckShape(raw *parser.AlterTable) error {
	reject := func() error { return sqlError("0A000", RestoreInheritedCheckBoundary) }
	if raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 {
		return reject()
	}
	action := raw.Actions[0]
	if action.Action != parser.AddCheck || action.Check == nil || action.Check.NotValid || action.Check.Name.Text == "" || action.IfExists || action.IfNotExists {
		return reject()
	}
	return dump.RestoreScalar(action.Check.Expression, true)
}

// InheritedCheckRecipe accepts the inherited-recipe CHECK on ref, a
// permanent ordinary table with a valid inheritance graph, only when one of
// repairs is still current, valid under relations, and connected to it.
func InheritedCheckRecipe(view catalog.Read, relations catalog.Provider, ref ids.ObjectRef, repairs []table.RestoredGenerationRecipe) error {
	reject := func() error { return sqlError("0A000", RestoreInheritedCheckBoundary) }
	target, ok := view.Relation(ref.Address)
	if !ok || target.RelationKind != catalog.TableRelation || target.Partition != nil || target.Membership != nil {
		return reject()
	}
	namespace, namespaceOK := view.Namespace(target.Namespace())
	if !namespaceOK || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if table.ValidateInheritance(view, target) != nil {
		return reject()
	}
	for _, repair := range repairs {
		if repair.Ref.Domain() != ref.Domain() || repair.Ref.Epoch != ref.Epoch {
			continue
		}
		tbl, ok := view.Relation(repair.Ref.Address)
		if !ok {
			continue
		}
		generation, ok := tbl.ColumnMeta.Generations[repair.Attribute]
		slot := tbl.IndexForAttribute(repair.Attribute)
		if !ok || slot < 0 || generation.OID != repair.Generation || generation.Kind != repair.Kind || tbl.Columns[slot].Usage != repair.Usage {
			continue
		}
		if relations.Validate(view, tbl) != nil {
			continue
		}
		if table.InheritedRecipeConnected(view, target.Address(), tbl.Address()) {
			return nil
		}
	}
	return reject()
}

// CloseRestoredRecipes drops from repairs, in place, the recipes connected
// to target, whose data Restore is about to load.
func CloseRestoredRecipes(view catalog.Read, target ids.ObjectAddress, repairs []table.RestoredGenerationRecipe) []table.RestoredGenerationRecipe {
	retained := repairs[:0]
	for _, repair := range repairs {
		if !table.InheritedRecipeConnected(view, target, repair.Ref.Address) {
			retained = append(retained, repair)
		}
	}
	return retained
}
