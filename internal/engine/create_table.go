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
func createTableLocked(e *Engine, scope *StatementScope, op *session.Operation, auth AuthorizationView, s *parser.CreateTable, count int) (Result, error) {
	if count != 0 {
		e.Mu.Unlock()
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	changes := scope.Owner.Catalog.Clone()
	persistence := s.Persistence
	if persistence == "" {
		persistence = "logged"
	}
	ns, err := e.RelationCreationNamespaceLocked(auth, s.Name, persistence, changes)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, ddl.CreationError(err, s.Name)
	}
	auth.Catalog = e.PrivateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	if table, ok := changes.Descriptors[ns.Address].(*catalog.Namespace); ok && !table.TemporaryToast && table.Temporary == op.Session.SessionToken {
		auth.StagedTemporary = table.Address()
	}
	if OperationReplayOrZero(op).TableDestination {
		bootstrap, er := auth.Roles.Descriptor(auth.State.Authenticated)
		if er != nil || !bootstrap.Superuser || e.ImportTargets == nil {
			e.Mu.Unlock()
			return Result{}, fmt.Errorf("stuntgres: invalid trusted restore destination authority")
		}
	}
	if err = auth.CheckCreate(ns); err != nil && !OperationReplayOrZero(op).TableDestination {
		e.Mu.Unlock()
		return Result{}, ddl.CreationError(err, s.Name)
	}
	persistence, err = access.AdjustRelationCreationPersistence(auth.View, auth, ns, persistence)
	if err != nil {
		e.Mu.Unlock()
		return Result{}, ddl.CreationError(err, s.Name)
	}
	temporary := persistence == "temporary"
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: s.Name.Parts[len(s.Name.Parts)-1].Text}
	_, exists := auth.Catalog.LookupName(key)
	if existing, err := table.CheckCreateShape(s, temporary, exists); existing || err != nil {
		e.Mu.Unlock()
		if existing {
			op.ExistingRelationNotice(key.Name)
		}
		return Result{}, err
	}
	base := auth.Catalog
	stage := e.BeginCatalogStage(base)
	stage.CreationName = s.Name
	defer e.DiscardCatalogStage(stage)
	stage.AuthorizeLocked(auth, op.Session)
	var partition *pendingPartitionDefinition
	if s.PartitionBy != nil || s.PartitionOf != nil {
		partition, err = schemaPartitionTableLocked(e, scope, auth, stage, s, ns.Address)
	} else {
		err = SchemaTableLocked(e, op, auth, stage, s, ns.Address, auth.State.CurrentUser, scope.Input, foreignKeysOrZero(scope).KeyTargets)
	}
	e.Mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	if err = partition.finish(scope); err != nil {
		return Result{}, err
	}
	// Only the source helper's newly acquired reservation is incidental.
	// Preserve entry admission and acquire genuine definition-provider holds
	// afterward, under their existing publication/cancellation policy.
	replacementRead := scope.ReplacementRead
	if err = acquireTypedSources(scope, stage, false); err != nil {
		return Result{}, err
	}
	if !replacementRead && scope.ReplacementRead {
		e.ReplacementMu.RUnlock()
		scope.ReplacementRead = false
	}
	if err = scope.AcquireDefinitionProviders(stage); err != nil {
		return Result{}, err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	createdAddress, _ := stage.NameBinding(key)
	created := e.Catalog.Domain.Ref(base.Epoch(), createdAddress)
	if temporary {
		scope.Owner.AccessedTemporary = true
		if len(stage.DomainBases) != 0 {
			// The validated stage no longer mutates these observations. Retain
			// them through Finish without copying them into the outer transaction.
			scope.CatalogAuthority = &session.CatalogAuthority{
				Session: stage.AuthSession, Authorization: stage.AuthRevision,
				Roles: stage.RoleRevision, Observations: stage.DomainBases, DatabaseAuthority: stage.DatabaseAuthority, DatabasePredecessor: stage.DatabasePredecessor,
			}
		}
		if err := e.StageCatalogOverlay(scope, stage, changes); err != nil {
			return Result{}, err
		}
		return Result{Created: created}, nil
	}
	_, err = e.PublishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: base.Epoch()})
	if err == nil {
		scope.Completed = true
	}
	if err != nil && s.IfNotExists {
		skipped, failure := scope.SkipConcurrentRelation(base, key, s.Name, err)
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
			e.Mu.Lock()
			currentAuth, authErr := e.AuthorizationLocked(op)
			if authErr != nil {
				e.Mu.Unlock()
				return Result{}, authErr
			}
			resolved, resolveErr := e.CreationNamespace(currentAuth, s.Name)
			_, exists := currentAuth.Catalog.LookupName(key)
			same := resolveErr == nil && resolved.Address == ns.Address && currentAuth.Catalog.Epoch() == base.Epoch()
			var accessErr error
			if same && exists {
				accessErr = currentAuth.CheckCreate(resolved)
			}
			contextErr := op.Context().Err()
			e.Mu.Unlock()
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
		return Result{}, SQLError("42P07", fmt.Sprintf(`relation "%s" already exists`, key.Name))
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Created: created}, nil
}

// SchemaTableLocked stages CREATE TABLE s without a PARTITION clause; see
// table.StageCreateTable. Called under Engine.mu.
func SchemaTableLocked(e *Engine, op *session.Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateTable, ns ids.ObjectAddress, owner authorization.PrincipalRef, input sqltype.InputEnvironment, foreignTargets []ids.ObjectRef) error {
	return table.StageCreateTable(TableHost{DDLHost{E: e, Op: op, CatalogStage: stage}}, auth, s, ns, owner, input, foreignTargets)
}
