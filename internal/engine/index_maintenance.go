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
	e.Mu.Lock()
	fail := func(err error) (Result, error) { e.Mu.Unlock(); return Result{}, err }
	if scope.TxRef.Epoch() != e.Catalog.State.Epoch() {
		return fail(ids.ErrCatalogReplaced)
	}
	auth, err := e.StatementAuthorizationLocked(scope.Op, stmt)
	if err != nil {
		return fail(err)
	}
	g := scope.IndexesOrZero().Maintenance
	if g == nil {
		return fail(fmt.Errorf("stuntgres: index utility has no relation admission"))
	}
	if err := g.Check(auth.View, auth, stmt, true); err != nil {
		return fail(err)
	}
	if err := scope.CaptureIdentityLocked(auth); err != nil {
		return fail(err)
	}
	_, reindex := stmt.(*parser.Reindex)
	if len(g.Targets) == 0 || reindex && len(g.Indexes) == 0 {
		scope.Op.Session.Mu.Lock()
		err := PublicationOperationLocked(e, scope.Op)
		scope.Op.Session.Mu.Unlock()
		e.Mu.Unlock()
		if err != nil {
			return Result{}, err
		}
		scope.Completed = true
		if reindex {
			scope.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`table "%s" has no indexes to reindex`, g.Descriptors[g.Targets[0].Address].Name())})
		}
		for _, notice := range g.Notices {
			scope.Op.EmitNotice(session.LevelNotice, notice)
		}
		return Result{}, nil
	}
	if s, ok := stmt.(*parser.AlterIndex); ok && s.Action == "rename" {
		return renameRelationLocked(e, scope, scope.Op, auth, s.Name, "", s.Target.Text, s.IfExists, false, false, false, count)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	stage.Indexes().Output = scope.Output
	if err := index.StageMaintenance(TableHost{DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, g, stmt); err != nil {
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		return Result{}, index.DefaultClassDiagnostic(DDLHost{E: e, Op: scope.Op, CatalogStage: stage}, auth, err)
	}
	e.Mu.Unlock()
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
		stage.DependencyPath, _ = ReferencePath(auth)
		if err := scope.StageExpressionDependents(stage, roots, drop.Mode, nil); err != nil {
			return Result{}, err
		}
	}
	if _, ok := stmt.(*parser.DropIndex); ok {
		if err := e.stagePartitionIndexValidity(stage, ids.ObjectAddress{}); err != nil {
			return Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, PublicationToken{Epoch: auth.Catalog.Epoch()})
	if err != nil {
		return Result{}, err
	}
	scope.Completed = true
	for _, notice := range g.Notices {
		scope.Op.EmitNotice(session.LevelNotice, notice)
	}
	return Result{}, nil
}

// validateIndexMaintenance forwards the stage's index observations and the
// original of a replaced materialized view to index.ValidateMaintenance.
func (s *CatalogStage) validateIndexMaintenance(current *catalog.State) error {
	var replacedParent ids.ObjectAddress
	if replacement := s.MaterializedOrZero().Replacement; replacement != nil {
		replacedParent = replacement.Original.Address()
	}
	return index.ValidateMaintenance(s.IndexesOrZero(), s.Base, current, replacedParent)
}

// indexes returns the statement's indexes slot, allocating it on first use.
func (s *StatementScope) indexes() *index.Scope {
	if s.indexesSlot == nil {
		s.indexesSlot = new(index.Scope)
	}
	return s.indexesSlot
}

// IndexesOrZero reads the indexes slot; a nil slot reads as zero.
func (s *StatementScope) IndexesOrZero() index.Scope {
	if s.indexesSlot == nil {
		return index.Scope{}
	}
	return *s.indexesSlot
}
