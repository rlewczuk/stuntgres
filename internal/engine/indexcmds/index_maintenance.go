package indexcmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// ExecuteIndexMaintenance runs ALTER INDEX, DROP INDEX and REINDEX over the
// targets the statement scope admitted; ALTER INDEX RENAME goes to
// tablecmds.RenameRelationLocked. It takes Engine.Mu itself.
func ExecuteIndexMaintenance(e *core.Engine, scope *core.StatementScope, stmt parser.Statement, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	fail := func(err error) (core.Result, error) { e.Mu.Unlock(); return core.Result{}, err }
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
		err := core.PublicationOperationLocked(e, scope.Op)
		scope.Op.Session.Mu.Unlock()
		e.Mu.Unlock()
		if err != nil {
			return core.Result{}, err
		}
		scope.CompleteUtility()
		if reindex {
			scope.Op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`table "%s" has no indexes to reindex`, g.Descriptors[g.Targets[0].Address].Name())})
		}
		for _, notice := range g.Notices {
			scope.Op.EmitNotice(session.LevelNotice, notice)
		}
		return core.Result{}, nil
	}
	if s, ok := stmt.(*parser.AlterIndex); ok && s.Action == "rename" {
		return tablecmds.RenameRelationLocked(e, scope, scope.Op, auth, s.Name, "", s.Target.Text, s.IfExists, false, false, false, count)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.AuthorizeLocked(auth, scope.Op.Session)
	stage.Indexes().Output = scope.Output
	if _, drop := stmt.(*parser.DropIndex); drop {
		path, err := core.ReferencePath(auth)
		if err != nil {
			e.Mu.Unlock()
			e.DiscardCatalogStage(stage)
			return core.Result{}, err
		}
		stage.DependencyPath = path
	}
	if err := index.StageMaintenance(tablecmds.TableHost{core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}}, g, stmt); err != nil {
		e.Mu.Unlock()
		e.DiscardCatalogStage(stage)
		return core.Result{}, index.DefaultClassDiagnostic(core.DDLHost{E: e, Op: scope.Op, CatalogStage: stage}, auth, err)
	}
	if reindex {
		for _, ref := range g.Indexes {
			idx := g.Descriptors[ref.Address]
			if stage.Indexes().Progress.Observer == nil {
				stage.Indexes().Progress = scope.BeginCreateIndexProgressLocked(idx.SQLIndex.Parent, idx.Address())
				scope.IndexProgressCommand("REINDEX", idx.Address().ObjectID)
				stage.Indexes().Progress.Targets = make(map[ids.ObjectAddress]ids.ObjectAddress, len(g.Indexes))
			}
			stage.Indexes().Progress.Targets[idx.Address()] = idx.SQLIndex.Parent
		}
	}
	e.Mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if drop, ok := stmt.(*parser.DropIndex); ok {
		for _, ref := range g.Indexes {
			if err := index.StagePartitionIndexDrop(stage, g.Descriptors[ref.Address], index.IndexDrivenDrop); err != nil {
				return core.Result{}, err
			}
		}
		for _, ref := range g.Targets {
			stage.MarkDrop(ref.Address, catalog.DropProvenance{Original: true})
		}
		roots := make([]ids.ObjectAddress, len(g.Indexes))
		for i, ref := range g.Indexes {
			roots[i] = ref.Address
		}
		if err := scope.StageExpressionDependents(stage, roots, drop.Mode, nil); err != nil {
			return core.Result{}, err
		}
	}
	if _, ok := stmt.(*parser.DropIndex); ok {
		if err := stagePartitionIndexValidity(e, stage, ids.ObjectAddress{}); err != nil {
			return core.Result{}, err
		}
	}
	prepared, err := e.ValidateCatalogStage(scope.Op.Context(), stage)
	if err != nil {
		return core.Result{}, err
	}
	_, err = e.PublishCatalogOperation(scope.Op, scope, prepared, core.PublicationToken{Epoch: auth.Catalog.Epoch()})
	if err != nil {
		return core.Result{}, err
	}
	if _, alter := stmt.(*parser.AlterIndex); alter {
		for _, ref := range g.Targets {
			scope.RecordDDLCommand("ALTER INDEX", ref.Address)
		}
	}
	if reindex {
		for _, ref := range g.Indexes {
			scope.RecordDDLCommand("REINDEX", ref.Address)
		}
	}
	scope.CompleteUtility()
	for _, notice := range g.Notices {
		scope.Op.EmitNotice(session.LevelNotice, notice)
	}
	return core.Result{}, nil
}
