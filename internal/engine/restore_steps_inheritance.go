package engine

import (
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Restore hook entries owned by inheritance: inherited foreign keys,
// generation recipes, historical constraint facts and inherited owners.
// Each calls the existing Engine method on the restore stage.

// beforeRestoreInheritedForeignKey captures an inherited foreign key ALTER
// for binding at finish, consuming the statement.
func beforeRestoreInheritedForeignKey(r *restoreRun, op *session.Operation, stmt parser.Statement) (bool, error) {
	alteration, ok := stmt.(*parser.AlterTable)
	if !ok || r.cur.physicalConstructor != nil {
		return false, nil
	}
	declaration, admitted, err := CaptureInheritedForeignKey(r.stage, op, alteration, r.historicalRefs)
	if err != nil || !admitted {
		return false, err
	}
	r.pendingForeignKeys = append(r.pendingForeignKeys, declaration)
	r.physicalForeignProvider = nil
	r.pairedNotNull = table.RestoredNotNull{}
	return true, nil
}

// prepareRestoreRecipeData closes the generation recipes an INSERT's rows
// complete.
func prepareRestoreRecipeData(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	insertion, ok := stmt.(*parser.Insert)
	if !ok || len(r.generationRepairs) == 0 {
		return nil
	}
	var err error
	r.generationRepairs, err = CloseRestoredRecipesForData(r.stage, op, insertion, r.generationRepairs)
	return err
}

// prepareRestoreGenerationPair pairs a SET EXPRESSION with its restored
// placeholder.
func prepareRestoreGenerationPair(r *restoreRun, op *session.Operation, stmt parser.Statement) error {
	if alteration, ok := restoreSetExpression(stmt); ok {
		return PairRestoredGeneration(r.stage, op, alteration, r.placeholderOrigins)
	}
	return nil
}

// afterRestoreHistoricalRelation records a created table and its historical
// constraint facts.
func afterRestoreHistoricalRelation(r *restoreRun, _ *session.Operation, stmt parser.Statement, outcome control.Outcome) error {
	if _, ok := stmt.(*parser.CreateTable); ok && outcome.Created != (ids.ObjectRef{}) {
		r.historicalRefs[outcome.Created.Address] = outcome.Created
		table.CaptureHistoricalRelation(r.stage.CatalogRead(), outcome.Created, r.historicalAuthority.Facts)
	}
	return nil
}

// afterRestoreGenerationPlaceholders records a created table's generation
// placeholders.
func afterRestoreGenerationPlaceholders(r *restoreRun, _ *session.Operation, stmt parser.Statement, outcome control.Outcome) error {
	if creation, ok := stmt.(*parser.CreateTable); ok && outcome.Created != (ids.ObjectRef{}) {
		table.CaptureRestoredPlaceholders(r.stage.CatalogRead(), outcome.Created, creation, r.placeholderOrigins)
	}
	return nil
}

// afterRestoreInheritedOwner pairs an inheriting table created under an
// owner directive with the ALTER OWNER that follows it.
func afterRestoreInheritedOwner(r *restoreRun, op *session.Operation, stmt parser.Statement, outcome control.Outcome) error {
	creation, ok := stmt.(*parser.CreateTable)
	if !ok || outcome.Created == (ids.ObjectRef{}) || r.cur.ownerForCreate == (authorization.PrincipalRef{}) || len(creation.Parents) == 0 {
		return nil
	}
	var err error
	r.pairedOwner, err = CaptureInheritedOwner(r.stage, op, outcome.Created, r.cur.ownerForCreate)
	return err
}

// afterRestoreHistoricalFacts refreshes the historical facts after an ALTER
// TABLE that adds CHECK or NOT NULL constraints.
func afterRestoreHistoricalFacts(r *restoreRun, _ *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	if alteration, altered := stmt.(*parser.AlterTable); altered && slices.ContainsFunc(alteration.Actions, func(a parser.AlterTableAction) bool {
		return a.Action == parser.AddCheck || a.Action == parser.AddNotNull || a.Action == parser.SetNotNull
	}) {
		table.CaptureHistoricalFacts(r.stage.CatalogRead(), r.historicalRefs, r.historicalAuthority.Facts)
	}
	return nil
}

// afterRestoreGeneration captures the recipe a SET EXPRESSION restored; the
// rows that follow close it.
func afterRestoreGeneration(r *restoreRun, op *session.Operation, stmt parser.Statement, _ control.Outcome) error {
	alteration, ok := restoreSetExpression(stmt)
	if !ok {
		return nil
	}
	record, err := CaptureRestoredGeneration(r.stage, op, alteration)
	if err != nil {
		return err
	}
	r.generationRepairs = append(r.generationRepairs, record)
	delete(r.placeholderOrigins, table.RestoredGenerationKey{Relation: record.Ref.Address, Attribute: record.Attribute})
	return nil
}

// restoreSetExpression reports whether stmt is a single-action ALTER TABLE
// SET EXPRESSION.
func restoreSetExpression(stmt parser.Statement) (*parser.AlterTable, bool) {
	alteration, ok := stmt.(*parser.AlterTable)
	if !ok || len(alteration.Actions) != 1 || alteration.Actions[0].Action != parser.SetExpression {
		return nil, false
	}
	return alteration, true
}
