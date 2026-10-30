package core

import (
	"context"
	"errors"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Source analysis and parameter import precede this destination-only phase.
// Engine.mu is held. A skipped destination never enters executor admission.
func (e *Engine) prepareQueryTableLocked(op *session.Operation, auth AuthorizationView, s *parser.CreateQueryTable, b analyze.Binding) (*CatalogStage, *catalog.Relation, bool, error) {
	ns, err := e.CreationNamespace(auth, s.Name)
	if err != nil {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "0A000" {
			err = ddl.CreationError(err, s.Name)
		}
		return nil, nil, false, err
	}
	if err = access.CheckPersistentCreationNamespace(auth.Catalog, ns.Address); err != nil {
		return nil, nil, false, err
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: name}
	if _, exists := auth.Catalog.LookupName(key); exists {
		if s.IfNotExists {
			op.ExistingRelationNotice(name)
			return nil, nil, true, nil
		}
		return nil, nil, false, SQLError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
	}
	if err = auth.CheckCreate(ns); err != nil {
		return nil, nil, false, err
	}
	if err := ddl.CheckDefaultTablespaceCreation(auth.View, auth, (DDLHost{E: e, Op: op}).DefaultTablespace(), "", false); err != nil {
		return nil, nil, false, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.CreationName = s.Name
	stage.AuthorizeLocked(auth, op.Session)
	available, err := e.DisplaceAssociatedArrayLocked(stage, ns.Address, name)
	if err != nil {
		return nil, nil, false, err
	}
	if !available {
		return nil, nil, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: fmt.Sprintf(`type "%s" already exists`, name), Hint: "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."}
	}
	columns, names, bases, err := table.QueryTableColumns(auth.View, auth, s, b)
	if err != nil {
		return nil, nil, false, err
	}
	stage.DomainBases = append(stage.DomainBases, bases...)
	// Source definitions and namespace lookup authority must still be current at
	// publication, including for NO DATA. Stored data has no dependency on them.
	for _, leaf := range b.QuerySources {
		stage.QuerySources = append(stage.QuerySources, leaf.Relation)
		if descriptor := auth.Catalog.Get(leaf.Relation.Namespace()); descriptor != nil {
			stage.QuerySources = append(stage.QuerySources, descriptor)
		}
	}
	owner := auth.State.CurrentUser
	if s.Form == parser.CreateMaterializedView && OperationReplayOrZero(op).TableDestination {
		directive, err := e.AuthorizationLocked(op)
		if err != nil {
			return nil, nil, false, err
		}
		owner = directive.State.CurrentUser
	}
	d, err := table.NewRelation(DDLHost{E: e}, ns.Address, name, owner, columns, names)
	if err != nil {
		return nil, nil, false, err
	}
	d.TableMethod, err = table.ResolveTableMethod(auth.Catalog, (DDLHost{E: e, Op: op}).DefaultTableAccessMethod())
	if err != nil {
		return nil, nil, false, err
	}
	if s.Form == parser.CreateMaterializedView {
		definition, err := view.FreezeDefinition(DDLHost{E: e}, auth.Catalog, CatalogSourceTemporaryOwner(auth), s.Query, b, false, false, 0, catalog.MaterializedViewRelation, false)
		if err != nil {
			return nil, nil, false, err
		}
		d.RelationKind, d.View, d.Populated = catalog.MaterializedViewRelation, stored.ViewPayload(definition), !s.NoData
	}
	d.Security.ACL = stage.CreationACL(owner, ns.Address, authorization.DefaultTables)
	if err = e.StageRowTypeLocked(stage, d); err != nil {
		return nil, nil, false, err
	}
	if err = stage.Create(d); err != nil {
		return nil, nil, false, err
	}
	if stage.OldNameCount() != 0 {
		if err = e.ReownStagedTypeGenerationsLocked(stage); err != nil {
			return nil, nil, false, err
		}
		d = stage.Get(d.Address()).(*catalog.Relation)
	}
	stage.RecordDDLCommand(session.ModifyingTag(s), d.Address())
	return stage, d, false, nil
}

// Rows have already been finalized, typed and detached from query scratch. Only
// this private stage owns them; identity gaps are allowed if publication fails.
func (e *Engine) StageQueryTableRows(ctx context.Context, stage *CatalogStage, d *catalog.Relation, rows [][]sqltype.Value, ref ids.TxRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if stage == nil || d == nil || stage.Owner != e || stage.Invalid != nil || stage.Lifecycle != CatalogOpen || stage.Get(d.Address()) != d || stage.Base.Get(d.Address()) != nil || stage.initialTables != nil {
		return errors.New("stuntgres: invalid initial table stage")
	}
	if stage.Base.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	// A demanded producer can enroll reads even when every row is filtered out.
	// NO DATA and existing-target skips never attach a producer transaction.
	if len(rows) == 0 {
		stage.InitialTx = ref
		return nil
	}
	for _, row := range rows {
		for i, value := range row {
			if target := d.Columns[i].Usage.Type; target != value.Type() {
				reowned, err := typedef.ReownDomainDatum(value, target, nil, nil, nil)
				if err != nil {
					return err
				}
				row[i] = reowned
			}
		}
	}
	versions, err := e.Storage.AllocateRowVersionsLocked(rows, nil)
	if err != nil {
		return err
	}
	stage.initialTables = map[ids.ObjectAddress]*storage.Relation{d.Address(): {Versions: versions}}
	stage.InitialTx = ref
	return nil
}

func (e *Engine) publishQueryTable(scope *StatementScope, stage *CatalogStage) error {
	op := scope.Op
	if err := scope.AcquireDefinitionProviders(stage); err != nil {
		return err
	}
	if scope.foreignTablesOrZero().owner != nil {
		if err := scope.finishForeignOwner(scope.foreignTablesOrZero().owner, nil); err != nil {
			return err
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		// Ordinary source functions, like writable CTEs, can leave row effects
		// in the shared statement stage without opening a catalog journal.
		// Publish the destination with those effects at Finish, not separately
		// followed by CompleteUtility suppressing their publication.
		if scope.cteInvocation != nil || len(scope.Stage.Rows) != 0 || len(scope.Stage.Deletes) != 0 {
			if scope.cteInvocation == nil {
				// Preserve the admissions and reservation release formerly owned
				// by PublishCatalogOperation on this non-CTE path.
				if err = scope.acquireCatalogStageAdmissions(stage); err != nil {
					return err
				}
				defer scope.releaseCatalogReservation()
			}
			err = e.StageCatalogOverlay(scope, stage, scope.PendingCatalog().Clone())
		} else {
			_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
		}
	}
	if err == nil && scope.cteInvocation == nil {
		scope.CompleteUtility()
	}
	return err
}

// The common SELECT pipeline has already planned and admitted this producer.
// Engine.mu is held on entry; every outcome releases it. The caller owns discard.
func (e *Engine) executeQueryTableLocked(scope *StatementScope, s *parser.CreateQueryTable, b analyze.Binding, root plan.Node, parameters []sqltype.Value, stage *CatalogStage, d *catalog.Relation) (Result, error) {
	ctx := scope.Op.Context()
	source, err := e.CaptureQuerySourcesLocked(scope, b.QuerySources, s.Query.From == nil && s.Query.Values == nil)
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	ctx = scope.ActivateRuntime(ctx)
	if err = exec.SelectQuerySources(ctx, source); err != nil {
		return Result{}, err
	}
	result, count, err := exec.Finalize(ctx, root, source, parameters)
	if err != nil {
		return Result{}, err
	}
	rows, err := result.(*exec.FinalRows).TakeTableRows(ctx)
	if err != nil {
		return Result{}, err
	}
	if err = e.StageQueryTableRows(ctx, stage, d, rows, scope.TxRef); err != nil {
		return Result{}, err
	}
	if err = e.publishQueryTable(scope, stage); err != nil {
		return Result{}, err
	}
	return Result{Count: int64(count)}, nil
}
