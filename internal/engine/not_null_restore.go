package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) captureRestoredNotNulls(created ids.ObjectRef, raw *parser.CreateTable, origins map[ids.OID]table.RestoredNotNull) {
	relation, ok := e.CatalogRead().Relation(created.Address)
	if !ok {
		return
	}
	capture := func(column, name string) {
		if name == "" {
			return
		}
		slot, ok := relation.Index[column]
		if !ok {
			return
		}
		attribute := relation.Attributes[slot]
		for _, constraint := range relation.Constraints.NotNull {
			if constraint.Name == name && constraint.Attribute == attribute && !constraint.NotValid {
				if graph := relation.Inheritance; graph != nil {
					fact := graph.NotNull[attribute]
					if !fact.Local || fact.Count() != 0 {
						return
					}
				}
				origins[constraint.OID] = table.RestoredNotNull{Ref: created, Attribute: attribute, Constraint: constraint.OID}
				return
			}
		}
	}
	for _, element := range raw.Elements {
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if constraint.NotNull {
					capture(element.Column.Name, constraint.Name.Text)
				}
			}
		}
		if element.NotNull != nil {
			capture(element.NotNull.Column.Text, element.NotNull.Name.Text)
		}
	}
}

func (e *Engine) pairRestoredNotNull(op *session.Operation, raw *parser.AlterTable, origins map[ids.OID]table.RestoredNotNull) (table.RestoredNotNull, error) {
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.SetNotNull {
		return table.RestoredNotNull{}, sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return table.RestoredNotNull{}, err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return table.RestoredNotNull{}, err
	}
	relation, ok := auth.Catalog.Relation(ref.Address)
	if !ok {
		return table.RestoredNotNull{}, sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	slot, ok := relation.Index[raw.Actions[0].Column.Text]
	if !ok {
		return table.RestoredNotNull{}, sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	for _, constraint := range relation.Constraints.NotNull {
		if constraint.Attribute != relation.Attributes[slot] || constraint.NotValid {
			continue
		}
		origin, exists := origins[constraint.OID]
		if exists && origin.Ref == ref && origin.Attribute == constraint.Attribute {
			return origin, nil
		}
	}
	return table.RestoredNotNull{}, sqlError("0A000", table.RestoreNoInheritBoundary)
}

// Unlike public UPDATE, this interpreter can only run inside isolated Restore,
// after an imported explicit validated local NN and its immediately paired SET.
func (e *Engine) restoreNoInheritNotNull(op *session.Operation, raw *parser.Update, origin table.RestoredNotNull) error {
	name, column, label, err := table.RestoreNoInheritSyntax(raw)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "permission denied for table pg_constraint")
	}
	e.mu.Lock()
	if !partitionConstraintReplayImport(e, op, auth) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore no-inherit authority")
	}
	ref, err := e.resolveRelationReference(auth, name, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := auth.Catalog.Relation(ref.Address)
	if !ok || old.RelationKind != catalog.TableRelation || origin.Ref != ref {
		e.mu.Unlock()
		return sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	slot, ok := old.Index[column]
	if !ok || old.Attributes[slot] != origin.Attribute || old.Columns[slot].Nullable {
		e.mu.Unlock()
		return sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	index := slices.IndexFunc(old.Constraints.NotNull, func(c catalog.NotNullConstraint) bool {
		return c.OID == origin.Constraint && c.Name == label && c.Attribute == origin.Attribute && !c.NotValid
	})
	if index < 0 {
		e.mu.Unlock()
		return sqlError("0A000", table.RestoreNoInheritBoundary)
	}
	for _, constraint := range old.Constraints.Unique {
		if constraint.Primary && slices.Contains(constraint.Attributes, origin.Attribute) {
			e.mu.Unlock()
			return sqlError("0A000", table.RestoreNoInheritBoundary)
		}
	}
	if graph := old.Inheritance; graph != nil {
		fact := graph.NotNull[origin.Attribute]
		if !fact.Local || fact.Count() != 0 {
			e.mu.Unlock()
			return sqlError("0A000", table.RestoreNoInheritBoundary)
		}
	}
	if old.Constraints.NotNull[index].NoInherit {
		e.mu.Unlock()
		return nil
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.ObjectRevision++
	next.Constraints.NotNull = slices.Clone(old.Constraints.NotNull)
	next.Constraints.NotNull[index].NoInherit = true
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err = stage.Replace(&next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return err
	}
	_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: ref.Epoch})
	return err
}
