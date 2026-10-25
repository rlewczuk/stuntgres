package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// Registration is initialization-only: the registry closes when New seeds the
// catalog. extension.Register holds the checks.
func (e *Engine) registerExtension(name string, implementation extension.Installer) error {
	if e.Catalog.State != nil {
		return fmt.Errorf("stuntgres: extension registry is closed")
	}
	registry, err := extension.Register(e.Catalog.Extensions, name, implementation)
	if err != nil {
		return err
	}
	e.Catalog.Extensions = registry
	return nil
}

func executeExtension(e *Engine, scope *StatementScope, s *parser.CreateExtension, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.Mu.Lock()
	stage, err := stageExtensionLocked(e, scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	return publishExtensionStage(e, scope, stage)
}

// CREATE and UPDATE publish through the same validated statement-owned changes.
// Provider callbacks run in ValidateCatalogStage outside the engine mutex.
func publishExtensionStage(e *Engine, scope *StatementScope, stage *CatalogStage) (Result, error) {
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(scope.Op.Context(), stage); err != nil {
		return Result{}, err
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
	return Result{}, nil
}

func stageExtensionLocked(e *Engine, scope *StatementScope, s *parser.CreateExtension) (*CatalogStage, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return nil, err
	}
	staged, err := extension.StageCreate(&extensionHost{engine: e, op: scope.Op, auth: auth}, auth, auth.View, s)
	if err != nil || staged == nil {
		return nil, err
	}
	return stageOf(staged)
}
