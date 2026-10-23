package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Registration is initialization-only: the registry closes when New seeds the
// catalog. extension.Register holds the checks.
func (e *Engine) registerExtension(name string, implementation extension.Installer) error {
	if e.catalog.state != nil {
		return fmt.Errorf("stuntgres: extension registry is closed")
	}
	registry, err := extension.Register(e.catalog.Extensions, name, implementation)
	if err != nil {
		return err
	}
	e.catalog.Extensions = registry
	return nil
}

func (e *Engine) executeExtension(scope *StatementScope, s *parser.CreateExtension, count int) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	e.mu.Lock()
	stage, err := e.stageExtensionLocked(scope, s)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	return e.publishExtensionStage(scope, stage)
}

// CREATE and UPDATE publish through the same validated statement-owned changes.
// Provider callbacks run in ValidateCatalogStage outside the engine mutex.
func (e *Engine) publishExtensionStage(scope *StatementScope, stage *CatalogStage) (Result, error) {
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(scope.op.Context(), stage); err != nil {
		return Result{}, err
	}
	e.mu.Lock()
	changes := scope.owner.Catalog.Clone()
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
		e.catalogChangeLocked(changes, a, d)
	}
	for key, original := range stage.Touched() {
		changes.Names[key] = original
	}
	changes.AddFence(extensionAuthorityFence{observation: extension.AuthorityObservation{Roles: stage.roleRevision, Namespace: stage.Base.Get(stage.extensionNamespace), Database: stage.databasePredecessor}})
	scope.catalog = changes
	scope.catalogAuthority = &catalogAuthority{session: scope.op.Session, authorization: stage.authRevision, roles: stage.roleRevision, ref: ids.ObjectRef{Address: e.catalog.owner().DatabaseAddress()}, descriptor: stage.databasePredecessor, databaseAuthority: stage.databaseAuthority, databasePredecessor: stage.databasePredecessor}
	e.mu.Unlock()
	return Result{}, nil
}

func (e *Engine) stageExtensionLocked(scope *StatementScope, s *parser.CreateExtension) (*CatalogStage, error) {
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return nil, err
	}
	staged, err := extension.StageCreate(&extensionHost{engine: e, op: scope.op, auth: auth}, auth, auth.View, s)
	if err != nil || staged == nil {
		return nil, err
	}
	return stageOf(staged)
}
