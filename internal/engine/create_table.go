package engine

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
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
	persistence := s.Persistence
	if persistence == "" {
		persistence = "logged"
	}
	ns, err := e.relationCreationNamespaceLocked(auth, s.Name, persistence, changes)
	if err != nil {
		e.mu.Unlock()
		return Result{}, ddl.CreationError(err, s.Name)
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
		return Result{}, ddl.CreationError(err, s.Name)
	}
	persistence, err = access.AdjustRelationCreationPersistence(auth.View, auth, ns, persistence)
	if err != nil {
		e.mu.Unlock()
		return Result{}, ddl.CreationError(err, s.Name)
	}
	temporary := persistence == "temporary"
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
	_, exists := auth.Catalog.LookupName(key)
	if existing, err := table.CheckCreateShape(s, temporary, exists); existing || err != nil {
		e.mu.Unlock()
		if existing {
			op.ExistingRelationNotice(key.Name)
		}
		return Result{}, err
	}
	base := auth.Catalog
	stage := e.BeginCatalogStage(base)
	stage.creationName = s.Name
	defer e.DiscardCatalogStage(stage)
	stage.authorizeLocked(auth, op.Session)
	var partition *pendingPartitionDefinition
	if s.PartitionBy != nil || s.PartitionOf != nil {
		partition, err = e.schemaPartitionTableLocked(scope, auth, stage, s, ns.Address)
	} else {
		err = e.schemaTableLocked(op, auth, stage, s, ns.Address, auth.State.CurrentUser, scope.input, scope.foreignKeysOrZero().KeyTargets)
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
	if err != nil && s.IfNotExists {
		skipped, failure := scope.skipConcurrentRelation(base, key, s.Name, err)
		if skipped {
			return Result{}, nil
		}
		err = failure
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
			_, exists := currentAuth.Catalog.LookupName(key)
			same := resolveErr == nil && resolved.Address == ns.Address && currentAuth.Catalog.Epoch() == base.Epoch()
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
				return Result{}, ddl.CreationError(resolveErr, s.Name)
			}
			if accessErr != nil {
				return Result{}, ddl.CreationError(accessErr, s.Name)
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
