package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func (e *Engine) captureRestoredPlaceholders(created ids.ObjectRef, raw *parser.CreateTable, origins map[restoredGenerationKey]restoredGenerationRecipe) {
	view := e.CatalogRead()
	table, ok := view.Relation(created.Address)
	if !ok {
		return
	}
	for attribute, definition := range table.ColumnMeta.Generations {
		column := table.Columns[table.IndexForAttribute(attribute)].Name
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
		if !declared && table.Inheritance != nil {
			inherited := false
			compatible := true
			for _, origin := range table.Inheritance.Columns[attribute].Origins {
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
				record, exists := origins[restoredGenerationKey{origin.Parent, origin.Attribute}]
				if !exists || record.Ref.Domain() != created.Domain() || record.Ref.Epoch != created.Epoch || record.Generation != generation.OID || record.Kind != generation.Kind || record.Usage != parent.Columns[parent.IndexForAttribute(origin.Attribute)].Usage {
					compatible = false
					break
				}
			}
			declared = inherited && compatible && !table.Inheritance.Columns[attribute].ExplicitGeneration
		}
		if declared {
			origins[restoredGenerationKey{created.Address, attribute}] = restoredGenerationRecipe{Ref: created, Attribute: attribute, Generation: definition.OID, Usage: table.Columns[table.IndexForAttribute(attribute)].Usage, Kind: definition.Kind}
		}
	}
}

func (e *Engine) pairRestoredGeneration(op *session.Operation, raw *parser.AlterTable, origins map[restoredGenerationKey]restoredGenerationRecipe) error {
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
	table, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return reject()
	}
	namespace, namespaceOK := auth.Catalog.Namespace(table.Namespace())
	if !namespaceOK || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	slot, exists := table.Index[raw.Actions[0].Column.Text]
	if !exists {
		return reject()
	}
	attribute := table.Attributes[slot]
	record, exists := origins[restoredGenerationKey{ref.Address, attribute}]
	definition, generated := table.ColumnMeta.Generations[attribute]
	if !exists || !generated || record.Ref != ref || record.Attribute != attribute || record.Generation != definition.OID || record.Usage != table.Columns[slot].Usage || record.Kind != definition.Kind {
		return reject()
	}
	if (relationProvider{}).Validate(auth.Catalog, table) != nil {
		return reject()
	}
	return nil
}

func (e *Engine) captureRestoredGeneration(op *session.Operation, raw *parser.AlterTable) (restoredGenerationRecipe, error) {
	auth, err := operationAuthorization(op)
	if err != nil {
		return restoredGenerationRecipe{}, err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return restoredGenerationRecipe{}, err
	}
	table, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return restoredGenerationRecipe{}, ids.ErrCatalogReplaced
	}
	slot, ok := table.Index[raw.Actions[0].Column.Text]
	if !ok {
		return restoredGenerationRecipe{}, ids.ErrCatalogReplaced
	}
	attribute := table.Attributes[slot]
	generation := table.ColumnMeta.Generations[attribute]
	return restoredGenerationRecipe{Ref: ref, Attribute: attribute, Generation: generation.OID, Usage: table.Columns[slot].Usage, Kind: generation.Kind}, nil
}

func (e *Engine) restoreInheritedCheck(op *session.Operation, raw *parser.AlterTable, repairs []restoredGenerationRecipe) error {
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
	if ValidateInheritance(auth.Catalog, target) != nil {
		return reject()
	}
	for _, repair := range repairs {
		if repair.Ref.Domain() != ref.Domain() || repair.Ref.Epoch != ref.Epoch {
			continue
		}
		table, ok := auth.Catalog.Relation(repair.Ref.Address)
		if !ok {
			continue
		}
		generation, ok := table.ColumnMeta.Generations[repair.Attribute]
		slot := table.IndexForAttribute(repair.Attribute)
		if !ok || slot < 0 || generation.OID != repair.Generation || generation.Kind != repair.Kind || table.Columns[slot].Usage != repair.Usage {
			continue
		}
		if (relationProvider{}).Validate(auth.Catalog, table) != nil {
			continue
		}
		if InheritedRecipeConnected(auth.Catalog, target.Address(), table.Address()) {
			return nil
		}
	}
	return reject()
}

func (e *Engine) closeRestoredRecipesForData(op *session.Operation, raw *parser.Insert, repairs []restoredGenerationRecipe) ([]restoredGenerationRecipe, error) {
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
		if !InheritedRecipeConnected(auth.Catalog, ref.Address, repair.Ref.Address) {
			retained = append(retained, repair)
		}
	}
	return retained, nil
}
