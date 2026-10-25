package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

func (e *Engine) pairRestoredGeneration(op *session.Operation, raw *parser.AlterTable, origins map[table.RestoredGenerationKey]table.RestoredGenerationRecipe) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	if !table.RestoredGenerationShape(raw) {
		return SQLError("0A000", table.RestoreGenerationBoundary)
	}
	auth, err := OperationAuthorization(op)
	if err != nil {
		return err
	}
	e.Mu.Lock()
	trusted := PartitionConstraintReplayImport(e, op, auth)
	e.Mu.Unlock()
	if !trusted {
		return SQLError("0A000", table.RestoreGenerationBoundary)
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	return table.PairRestoredGeneration(auth.Catalog, relationProvider, ref, raw, origins)
}

func (e *Engine) captureRestoredGeneration(op *session.Operation, raw *parser.AlterTable) (table.RestoredGenerationRecipe, error) {
	auth, err := OperationAuthorization(op)
	if err != nil {
		return table.RestoredGenerationRecipe{}, err
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return table.RestoredGenerationRecipe{}, err
	}
	return table.CaptureRestoredGeneration(auth.Catalog, ref, raw)
}

// restoreCheck admits real valid CHECK additions in the private restore stage.
// ONLY local additions cannot propagate to descendants or merge inherited checks.
// Recursive generation replay retains connected repair authority. Fresh
// recursive declarations require absent names throughout the affected closure.
// Binding, ownership, row validation and publication belong to ordinary ALTER.
func (e *Engine) restoreCheck(op *session.Operation, raw *parser.AlterTable, repairs []table.RestoredGenerationRecipe) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	if err := table.RestoredCheckShape(raw); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.StatementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !PartitionConstraintReplayImport(e, op, auth) {
		return SQLError("0A000", table.RestoreInheritedCheckBoundary)
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	target, ok := auth.Catalog.Relation(ref.Address)
	if !ok || target.RelationKind != catalog.TableRelation || target.Partition != nil || target.Membership != nil {
		return SQLError("0A000", table.RestoreInheritedCheckBoundary)
	}
	namespace, exists := auth.Catalog.Namespace(target.Namespace())
	if !exists || namespace.Temporary != (ids.SessionToken{}) {
		return SQLError("0A000", table.RestoreInheritedCheckBoundary)
	}
	inherited := catalog.InheritanceParticipant(auth.Catalog, target)
	if raw.Only {
		if !inherited || !raw.Actions[0].Check.NoInherit && catalog.InheritanceHasChildren(auth.Catalog, target.Address()) {
			return SQLError("0A000", table.RestoreInheritedCheckBoundary)
		}
		for _, check := range target.Constraints.Check {
			if check.Name == raw.Actions[0].Check.Name.Text {
				// An existing inherited CHECK must not be merged into a local
				// declaration merely to authorize a historical replay window.
				return SQLError("0A000", table.RestoreInheritedCheckBoundary)
			}
		}
		return table.ValidateInheritance(auth.Catalog, target)
	}
	if !inherited {
		return nil
	}
	connectedRepair := false
	for _, repair := range repairs {
		if table.InheritedRecipeConnected(auth.Catalog, target.Address(), repair.Ref.Address) {
			connectedRepair = true
			break
		}
	}
	if !connectedRepair && !raw.Actions[0].Check.NoInherit && catalog.InheritanceHasChildren(auth.Catalog, target.Address()) {
		// Reuse the real propagation closure, but admit only new definitions.
		// No existing local or inherited CHECK can be merged by this path.
		for _, member := range table.InheritanceAlterOrder(auth.Catalog, target) {
			if err := op.Context().Err(); err != nil {
				return err
			}
			namespace, exists := auth.Catalog.Namespace(member.Namespace())
			if member.RelationKind != catalog.TableRelation || member.Partition != nil || member.Membership != nil || !exists || namespace.Temporary != (ids.SessionToken{}) {
				return SQLError("0A000", table.RestoreInheritedCheckBoundary)
			}
			if err := relationProvider.Validate(auth.Catalog, member); err != nil {
				return err
			}
			if err := table.ValidateInheritance(auth.Catalog, member); err != nil {
				return err
			}
			for _, check := range member.Constraints.Check {
				if check.Name == raw.Actions[0].Check.Name.Text {
					return SQLError("0A000", table.RestoreInheritedCheckBoundary)
				}
			}
		}
		return nil
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return SQLError("0A000", table.RestoreInheritedCheckBoundary)
	}
	return table.InheritedCheckRecipe(auth.Catalog, relationProvider, ref, repairs)
}

func (e *Engine) closeRestoredRecipesForData(op *session.Operation, raw *parser.Insert, repairs []table.RestoredGenerationRecipe) ([]table.RestoredGenerationRecipe, error) {
	auth, err := OperationAuthorization(op)
	if err != nil {
		return nil, err
	}
	ref, err := e.ResolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	return table.CloseRestoredRecipes(auth.Catalog, ref.Address, repairs), nil
}
