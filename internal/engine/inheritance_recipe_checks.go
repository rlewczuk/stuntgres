package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) captureRestoredPlaceholders(created ids.ObjectRef, raw *parser.CreateTable, origins map[table.RestoredGenerationKey]table.RestoredGenerationRecipe) {
	view := e.CatalogRead()
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

func (e *Engine) pairRestoredGeneration(op *session.Operation, raw *parser.AlterTable, origins map[table.RestoredGenerationKey]table.RestoredGenerationRecipe) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error {
		return sqlError("0A000", "restore accepts only paired generated-placeholder recipe definitions")
	}
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.SetExpression {
		return reject()
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	trusted := partitionConstraintReplayImport(e, op, auth)
	e.mu.Unlock()
	if !trusted {
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	tbl, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return reject()
	}
	namespace, namespaceOK := auth.Catalog.Namespace(tbl.Namespace())
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
	if (relationProvider{}).Validate(auth.Catalog, tbl) != nil {
		return reject()
	}
	return nil
}

func (e *Engine) captureRestoredGeneration(op *session.Operation, raw *parser.AlterTable) (table.RestoredGenerationRecipe, error) {
	auth, err := operationAuthorization(op)
	if err != nil {
		return table.RestoredGenerationRecipe{}, err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return table.RestoredGenerationRecipe{}, err
	}
	tbl, ok := auth.Catalog.Relation(ref.Address)
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

func (e *Engine) restoreInheritedCheck(op *session.Operation, raw *parser.AlterTable, repairs []table.RestoredGenerationRecipe) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error {
		return sqlError("0A000", "restore accepts only paired inherited-recipe CHECK definitions")
	}
	if raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 {
		return reject()
	}
	action := raw.Actions[0]
	if action.Action != parser.AddCheck || action.Check == nil || action.Check.NotValid || action.Check.Name.Text == "" || action.IfExists || action.IfNotExists {
		return reject()
	}
	if err := dump.RestoreScalar(action.Check.Expression, true); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !partitionConstraintReplayImport(e, op, auth) {
		return reject()
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	target, ok := auth.Catalog.Relation(ref.Address)
	if !ok || target.RelationKind != catalog.TableRelation || target.Partition != nil || target.Membership != nil {
		return reject()
	}
	namespace, namespaceOK := auth.Catalog.Namespace(target.Namespace())
	if !namespaceOK || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if table.ValidateInheritance(auth.Catalog, target) != nil {
		return reject()
	}
	for _, repair := range repairs {
		if repair.Ref.Domain() != ref.Domain() || repair.Ref.Epoch != ref.Epoch {
			continue
		}
		tbl, ok := auth.Catalog.Relation(repair.Ref.Address)
		if !ok {
			continue
		}
		generation, ok := tbl.ColumnMeta.Generations[repair.Attribute]
		slot := tbl.IndexForAttribute(repair.Attribute)
		if !ok || slot < 0 || generation.OID != repair.Generation || generation.Kind != repair.Kind || tbl.Columns[slot].Usage != repair.Usage {
			continue
		}
		if (relationProvider{}).Validate(auth.Catalog, tbl) != nil {
			continue
		}
		if table.InheritedRecipeConnected(auth.Catalog, target.Address(), tbl.Address()) {
			return nil
		}
	}
	return reject()
}

func (e *Engine) closeRestoredRecipesForData(op *session.Operation, raw *parser.Insert, repairs []table.RestoredGenerationRecipe) ([]table.RestoredGenerationRecipe, error) {
	auth, err := operationAuthorization(op)
	if err != nil {
		return nil, err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	retained := repairs[:0]
	for _, repair := range repairs {
		if !table.InheritedRecipeConnected(auth.Catalog, ref.Address, repair.Ref.Address) {
			retained = append(retained, repair)
		}
	}
	return retained, nil
}
