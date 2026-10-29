package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
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
	stage, err := stageExtensionLocked(e, scope, s)
	e.Mu.Unlock()
	if err != nil || stage == nil {
		return core.Result{}, err
	}
	return publishExtensionStage(e, scope, stage)
}

// Extension commands publish through the same validated statement-owned changes.
// Provider callbacks run in ValidateCatalogStage outside the engine mutex.
func publishExtensionStage(e *core.Engine, scope *core.StatementScope, stage *core.CatalogStage) (core.Result, error) {
	defer e.DiscardCatalogStage(stage)
	if _, err := e.ValidateCatalogStage(scope.Op.Context(), stage); err != nil {
		return core.Result{}, err
	}
	e.Mu.Lock()
	defer e.Mu.Unlock()
	changes := scope.PendingCatalog().Clone()
	capture := session.JournalCapture{Journal: changes, Candidate: &stage.Candidate, Committed: e.Catalog.State, Current: e.PrivateCatalogLocked(scope.PendingCatalog()).State()}
	if err := capture.ObserveCandidate(); err != nil {
		return core.Result{}, err
	}
	if err := capture.FenceNames(nil); err != nil {
		return core.Result{}, err
	}
	for a, d := range stage.Descriptors() {
		if _, extension := d.(*catalog.Extension); extension {
			if old, exists := stage.Base.Extension(a); exists {
				for _, member := range old.Members {
					if err := capture.ObserveBase(member); err != nil {
						return core.Result{}, err
					}
				}
			}
		}
	}
	namespace := extensionNamespaceOf(stage)
	if err := capture.ObserveBase(namespace); err != nil {
		return core.Result{}, err
	}
	if h, ok := stage.ExtensionSlot.(*extensionHost); ok && h.predecessor != nil {
		if err := capture.ObserveBase(h.predecessor.Address()); err != nil {
			return core.Result{}, err
		}
	}
	for a, d := range stage.Descriptors() {
		e.CatalogChangeLocked(changes, a, d)
	}
	// Candidate observations use the private predecessor; journal fences
	// retain committed roots, including names already changed by this TX.
	changes.AddFence(extensionAuthorityFence{observation: extension.AuthorityObservation{Roles: stage.RoleRevision, Namespace: e.Catalog.State.Get(namespace), Database: stage.DatabasePredecessor}})
	if err := e.CatalogPreflightLocked(changes); err != nil {
		return core.Result{}, err
	}
	scope.Catalog = changes
	scope.CatalogAuthority = &session.CatalogAuthority{Session: scope.Op.Session, Authorization: stage.AuthRevision, Roles: stage.RoleRevision, Ref: ids.ObjectRef{Address: e.Catalog.Owner.DatabaseAddress()}, Descriptor: stage.DatabasePredecessor, DatabaseAuthority: stage.DatabaseAuthority, DatabasePredecessor: stage.DatabasePredecessor}
	if scope.EventCommand != nil && scope.EventCommand.CollectDDL {
		scope.EventCommand.Commands = append(scope.EventCommand.Commands, stage.DDLCommands...)
	}
	return core.Result{}, nil
}

// stageExtensionLocked stages CREATE EXTENSION in a new catalog stage under
// Engine.Mu, for ExecuteExtension to publish.
func stageExtensionLocked(e *core.Engine, scope *core.StatementScope, s *parser.CreateExtension) (*core.CatalogStage, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return nil, err
	}
	staged, err := extension.StageCreate(&extensionHost{Engine: e, Op: scope.Op, Auth: auth}, auth, auth.View, s)
	if err != nil || staged == nil {
		return nil, err
	}
	stage, err := stageOf(staged)
	if err == nil {
		ddl.RecordCommand(stage, "CREATE EXTENSION", stage.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}))
	}
	return stage, err
}
