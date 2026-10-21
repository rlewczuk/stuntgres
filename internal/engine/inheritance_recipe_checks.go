package engine

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) pairRestoredGeneration(op *session.Operation, raw *parser.AlterTable, origins map[table.RestoredGenerationKey]table.RestoredGenerationRecipe) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	if !RestoredGenerationShape(raw) {
		return sqlError("0A000", RestoreGenerationBoundary)
	}
	auth, err := operationAuthorization(op)
	if err != nil {
		return err
	}
	e.mu.Lock()
	trusted := partitionConstraintReplayImport(e, op, auth)
	e.mu.Unlock()
	if !trusted {
		return sqlError("0A000", RestoreGenerationBoundary)
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	return PairRestoredGeneration(auth.Catalog, relationProvider{}, ref, raw, origins)
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
	return CaptureRestoredGeneration(auth.Catalog, ref, raw)
}

func (e *Engine) restoreInheritedCheck(op *session.Operation, raw *parser.AlterTable, repairs []table.RestoredGenerationRecipe) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	if err := RestoredInheritedCheckShape(raw); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !partitionConstraintReplayImport(e, op, auth) {
		return sqlError("0A000", RestoreInheritedCheckBoundary)
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("0A000", RestoreInheritedCheckBoundary)
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	return InheritedCheckRecipe(auth.Catalog, relationProvider{}, ref, repairs)
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
	return CloseRestoredRecipes(auth.Catalog, ref.Address, repairs), nil
}
