package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func (e *Engine) executeIndexMaintenance(scope *StatementScope, stmt parser.Statement, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if scope.txRef.Epoch() != e.catalog.state.Epoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	auth, err := e.statementAuthorizationLocked(scope.op, stmt)
	if err != nil {
		return fail(err)
	}
	g := scope.indexesOrZero().Maintenance
	if g == nil {
		return fail(fmt.Errorf("stuntgres: index utility has no relation admission"))
	}
	if err := g.Check(auth.View, auth, stmt, true); err != nil {
		return fail(err)
	}
	if err := scope.captureIdentityLocked(auth); err != nil {
		return fail(err)
	}
	_, reindex := stmt.(*parser.Reindex)
	if len(g.Targets) == 0 || reindex && len(g.Indexes) == 0 {
		scope.op.Session.Mu.Lock()
		err := publicationOperationLocked(e, scope.op)
		scope.op.Session.Mu.Unlock()
		e.mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.completed = true
		if reindex {
			scope.op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`table "%s" has no indexes to reindex`, g.Descriptors[g.Targets[0].Address].Name())})
		}
		for _, notice := range g.Notices {
			scope.op.EmitNotice(session.LevelNotice, notice)
		}
		return Result{}, nil
	}
	if s, ok := stmt.(*parser.AlterIndex); ok && s.Action == "rename" {
		return e.renameRelationLocked(scope, scope.op, auth, s.Name, "", s.Target.Text, s.IfExists, false, false, false, count)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, scope.op.Session)
	stage.indexes().Output = scope.output
	if err := index.StageMaintenance(ddlHost{e: e, op: scope.op, stage: stage}, g, stmt); err != nil {
		e.mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, err
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if drop, ok := stmt.(*parser.DropIndex); ok {
		for _, ref := range g.Indexes {
			if err := index.StagePartitionIndexDrop(stage, g.Descriptors[ref.Address]); err != nil {
				return Result{}, err
			}
		}
		roots := make([]ids.ObjectAddress, len(g.Indexes))
		for i, ref := range g.Indexes {
			roots[i] = ref.Address
		}
		stage.dependencyPath, _ = referencePath(auth)
		if err := scope.stageExpressionDependents(stage, roots, drop.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	if _, ok := stmt.(*parser.DropIndex); ok {
		if err := e.stagePartitionIndexValidity(stage, ids.ObjectAddress{}); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(scope.op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(scope.op, scope, prepared, PublicationToken{Epoch: auth.Catalog.Epoch()})
	if err != nil {
		return Result{}, err
	}
	scope.completed = true
	for _, notice := range g.Notices {
		scope.op.EmitNotice(session.LevelNotice, notice)
	}
	return Result{}, nil
}

// validateIndexMaintenance forwards the stage's index observations and the
// original of a replaced materialized view to index.ValidateMaintenance.
func (s *CatalogStage) validateIndexMaintenance(current *catalog.State) error {
	var replacedParent ids.ObjectAddress
	if replacement := s.materializedOrZero().Replacement; replacement != nil {
		replacedParent = replacement.Original.Address()
	}
	return index.ValidateMaintenance(s.indexesOrZero(), s.Base, current, replacedParent)
}

// indexes returns the statement's indexes slot, allocating it on first use.
func (s *StatementScope) indexes() *index.Scope {
	if s.indexesSlot == nil {
		s.indexesSlot = new(index.Scope)
	}
	return s.indexesSlot
}

// indexesOrZero reads the indexes slot; a nil slot reads as zero.
func (s *StatementScope) indexesOrZero() index.Scope {
	if s.indexesSlot == nil {
		return index.Scope{}
	}
	return *s.indexesSlot
}
