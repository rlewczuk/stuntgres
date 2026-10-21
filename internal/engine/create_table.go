package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Called under Engine.mu. Every return releases it, like the existing DDL path.
func (e *Engine) createTableLocked(scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.CreateTable, count int) (Result, error) {
	if count != 0 {
		e.mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	changes := scope.owner.Catalog.Clone()
	originalCatalog := auth.Catalog
	persistence := s.Persistence
	if persistence == "" {
		persistence = "logged"
	}
	ns, err := e.relationCreationNamespaceLocked(auth, s.Name, persistence, changes)
	if err != nil {
		e.mu.Unlock()
		return Result{}, creationError(err, s.Name)
	}
	auth.Catalog = e.privateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
		auth.stagedTemporary = table.Address()
	}
	if operationReplayOrZero(op).tableDestination {
		bootstrap, er := auth.Roles.Descriptor(auth.State.Authenticated)
		if er != nil || !bootstrap.Superuser || e.importTargets == nil {
			e.mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: invalid trusted restore destination authority")
		}
	}
	if err = auth.CheckCreate(ns); err != nil && !operationReplayOrZero(op).tableDestination {
		e.mu.Unlock()
		return Result{}, creationError(err, s.Name)
	}
	persistence, err = adjustRelationCreationPersistence(auth, ns, persistence)
	if err != nil {
		e.mu.Unlock()
		return Result{}, creationError(err, s.Name)
	}
	temporary := persistence == "temporary"
	if temporary && (s.PartitionBy != nil || s.PartitionOf != nil) {
		e.mu.Unlock()
		pos := s.PartitionBy
		if s.PartitionOf != nil {
			return Result{}, expr.ErrorAt("0A000", "temporary partition definitions are unsupported", s.PartitionOf.Pos)
		}
		return Result{}, expr.ErrorAt("0A000", "temporary partition definitions are unsupported", pos.Pos)
	}
	if !temporary {
		// Permanent CREATE does not need the TEMP namespace overlay. Preserve
		// its admitted catalog identity for the existing importer/publication fences.
		auth.Catalog = originalCatalog
		auth.Recapture(auth.Catalog)
	}
	if !temporary && s.OnCommit != parser.OnCommitUnspecified {
		e.mu.Unlock()
		return Result{}, sqlError("42P16", "ON COMMIT can only be used on temporary tables")
	}
	if !temporary && scope.owner.State == session.TxActive {
		e.mu.Unlock()
		return Result{}, sqlError("0A000", "statement is unsupported in an explicit transaction")
	}
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
	if _, exists := auth.Catalog.LookupName(key); exists {
		e.mu.Unlock()
		if s.IfNotExists {
			op.ExistingRelationNotice(key.Name)
			return Result{}, nil
		}
		return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
	}
	if s.PartitionBy != nil && len(s.Parents) != 0 {
		e.mu.Unlock()
		return Result{}, sqlError("42P17", "cannot create partitioned table as inheritance child")
	}
	base := auth.Catalog
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	stage.authorizeLocked(auth, op.Session)
	var partition *pendingPartitionDefinition
	if s.PartitionBy != nil || s.PartitionOf != nil {
		partition, err = e.schemaPartitionTableLocked(scope, auth, stage, s, ns.Address)
	} else {
		err = e.schemaTableLocked(op, auth, stage, s, ns.Address, auth.State.CurrentUser, scope.input, scope.foreignActionsOrZero().KeyTargets)
	}
	e.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = partition.finish(scope); err != nil {
		return Result{}, err
	}
	// Only the source helper's newly acquired reservation is incidental.
	// Preserve entry admission and acquire genuine definition-provider holds
	// afterward, under their existing publication/cancellation policy.
	replacementRead := scope.replacementRead
	if err = scope.acquireTypedSources(stage, false); err != nil {
		return Result{}, err
	}
	if !replacementRead && scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	if err = scope.acquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	createdAddress, _ := stage.NameBinding(key)
	created := e.catalog.Domain.Ref(base.Epoch(), createdAddress)
	if temporary {
		scope.owner.AccessedTemporary = true
		if len(stage.domainBases) != 0 {
			// The validated stage no longer mutates these observations. Retain
			// them through Finish without copying them into the outer transaction.
			scope.catalogAuthority = &catalogAuthority{
				session: stage.authSession, authorization: stage.authRevision,
				roles: stage.roleRevision, observations: stage.domainBases, databaseAuthority: stage.databaseAuthority, databasePredecessor: stage.databasePredecessor,
			}
		}
		if err := e.stageCatalogOverlay(scope, stage, changes); err != nil {
			return Result{}, err
		}
		return Result{created: created}, nil
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.completed = true
	}
	if _, conflict := err.(catalog.Conflict); conflict {
		if err := op.Context().Err(); err != nil {
			return Result{}, err
		}
		if s.IfNotExists {
			e.mu.Lock()
			currentAuth, authErr := e.authorizationLocked(op)
			if authErr != nil {
				e.mu.Unlock()
				return Result{}, authErr
			}
			resolved, resolveErr := e.creationNamespace(currentAuth, s.Name)
			_, exists := e.catalog.state.LookupName(key)
			same := resolveErr == nil && resolved.Address == ns.Address && e.catalog.state.Epoch() == base.Epoch()
			var accessErr error
			if same && exists {
				accessErr = currentAuth.CheckCreate(resolved)
			}
			contextErr := op.Context().Err()
			e.mu.Unlock()
			if contextErr != nil {
				return Result{}, contextErr
			}
			if resolveErr != nil {
				return Result{}, creationError(resolveErr, s.Name)
			}
			if accessErr != nil {
				return Result{}, creationError(accessErr, s.Name)
			}
			if same && exists {
				op.ExistingRelationNotice(key.Name)
				return Result{}, nil
			}
		}
		return Result{}, sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
	}
	if err != nil {
		return Result{}, err
	}
	return Result{created: created}, nil
}

// schemaTableLocked stages CREATE TABLE s without a PARTITION clause; see
// table.StageCreateTable. Called under Engine.mu.
func (e *Engine) schemaTableLocked(op *session.Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateTable, ns ids.ObjectAddress, owner authorization.PrincipalRef, input sqltype.InputEnvironment, foreignTargets []ids.ObjectRef) error {
	return table.StageCreateTable(ddlHost{e: e, op: op, stage: stage}, auth, s, ns, owner, input, foreignTargets)
}
