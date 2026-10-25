package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// ExecuteExtension runs CREATE EXTENSION: it stages the bundled provider
// under Engine.Mu and publishes the stage after its provider validation,
// which runs with the mutex released.
func ExecuteExtension(e *core.Engine, scope *core.StatementScope, s *parser.CreateExtension, count int) (core.Result, error) {
	if count != 0 {
		return core.Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	stage, err := StageExtensionLocked(e, scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		return core.Result{}, err
	}
	return publishExtensionStage(e, scope, stage)
}

// CREATE and UPDATE publish through the same validated statement-owned changes.
// Provider callbacks run in ValidateCatalogStage outside the engine mutex.
func publishExtensionStage(e *core.Engine, scope *core.StatementScope, stage *core.CatalogStage) (core.Result, error) {
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(scope.Op.Context(), stage); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	changes := scope.Owner.Catalog.Clone()
	for a, d := range stage.Descriptors() {
		// Validation ran without the mutex. Capture the stage's exact
		// predecessor, never whatever a peer published during validation.
		if _, captured := changes.Originals[a]; !captured {
			changes.Originals[a] = stage.Base.Get(a)
		}
		if _, extension := d.(*catalog.Extension); extension {
			if old, exists := stage.Base.Extension(a); exists {
				for _, member := range old.Members {
					if _, captured := changes.Originals[member]; !captured {
						changes.Originals[member] = stage.Base.Get(member)
					}
				}
			}
		}
		e.CatalogChangeLocked(changes, a, d)
	}
	for key, original := range stage.Touched() {
		changes.Names[key] = original
	}
	changes.AddFence(extensionAuthorityFence{observation: extension.AuthorityObservation{Roles: stage.RoleRevision, Namespace: stage.Base.Get(extensionNamespaceOf(stage)), Database: stage.DatabasePredecessor}})
	scope.Catalog = changes
	scope.CatalogAuthority = &session.CatalogAuthority{Session: scope.Op.Session, Authorization: stage.AuthRevision, Roles: stage.RoleRevision, Ref: ids.ObjectRef{Address: e.Catalog.Owner.DatabaseAddress()}, Descriptor: stage.DatabasePredecessor, DatabaseAuthority: stage.DatabaseAuthority, DatabasePredecessor: stage.DatabasePredecessor}
	e.Mu.Unlock()
	return core.Result{}, nil
}

// StageExtensionLocked stages CREATE EXTENSION in a new catalog stage under
// Engine.Mu, for ExecuteExtension to publish.
func StageExtensionLocked(e *core.Engine, scope *core.StatementScope, s *parser.CreateExtension) (*core.CatalogStage, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return nil, err
	}
	staged, err := extension.StageCreate(&extensionHost{Engine: e, Op: scope.Op, Auth: auth}, auth, auth.View, s)
	if err != nil || staged == nil {
		return nil, err
	}
	return stageOf(staged)
}
