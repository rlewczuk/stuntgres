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
	initializingPgcrypto := s.Name.Text == "pgcrypto" && !scope.Op.Session.Settings.PgcryptoInitialized()
	if initializingPgcrypto {
		scope.Owner.Mu.Lock()
	}
	e.Mu.Lock()
	stage, err := stageExtensionLocked(e, scope, s)
	e.Mu.Unlock()
	if initializingPgcrypto {
		scope.Owner.Mu.Unlock()
	}
	if err != nil {
		// Staging and materialization can fail before publication owns the
		// stage. Discard only after releasing both mutexes: disposal can lock.
		e.DiscardCatalogStage(stage)
		return core.Result{}, err
	}
	if stage == nil {
		return core.Result{}, nil
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
	changes, err := prepareExtensionStageChangesLocked(e, scope, stage)
	e.Mu.Unlock()
	if err != nil {
		return core.Result{}, err
	}
	if err := e.StageCatalogOverlay(scope, stage, changes); err != nil {
		return core.Result{}, err
	}
	return core.Result{}, nil
}

// Extension-specific observations supplement the ordinary validated catalog
// overlay. Physical index preparation and final publication remain owned by
// StageCatalogOverlay, outside this observation lock.
func prepareExtensionStageChangesLocked(e *core.Engine, scope *core.StatementScope, stage *core.CatalogStage) (*session.Journal, error) {
	if err := scope.Op.Context().Err(); err != nil {
		return nil, err
	}
	changes := scope.PendingCatalog().Clone()
	capture := session.JournalCapture{Journal: changes, Candidate: &stage.Candidate, Committed: e.Catalog.State, Current: e.PrivateCatalogLocked(scope.PendingCatalog()).State()}
	if err := capture.ObserveCandidate(); err != nil {
		return nil, err
	}
	if err := capture.FenceNames(nil); err != nil {
		return nil, err
	}
	for a, d := range stage.Descriptors() {
		if _, extension := d.(*catalog.Extension); extension {
			if old, exists := stage.Base.Extension(a); exists {
				for _, member := range old.Members {
					if err := capture.ObserveBase(member); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	namespaces := extensionNamespacesOf(stage)
	for _, observed := range namespaces {
		if e.Catalog.State.Get(observed.Address) != observed.Predecessor {
			return nil, catalog.Conflict{}
		}
		if original, exists := changes.Originals[observed.Address]; exists && original != observed.Predecessor {
			return nil, catalog.Conflict{}
		}
		if err := capture.ObserveBase(observed.Address); err != nil {
			return nil, err
		}
		changes.Originals[observed.Address] = observed.Predecessor
	}
	if h, ok := stage.ExtensionSlot.(*extensionHost); ok {
		for _, observed := range h.prerequisites {
			address := observed.descriptor.Address()
			if e.Catalog.State.Get(address) != observed.predecessor {
				return nil, catalog.Conflict{}
			}
			if original, exists := changes.Originals[address]; exists && original != observed.predecessor {
				return nil, catalog.Conflict{}
			}
			if err := capture.Observe(address, observed.descriptor); err != nil {
				return nil, err
			}
			changes.Originals[address] = observed.predecessor
			for _, member := range observed.descriptor.Members {
				if err := capture.ObserveBase(member); err != nil {
					return nil, err
				}
			}
		}
	}
	if h, ok := stage.ExtensionSlot.(*extensionHost); ok && h.predecessor != nil {
		if err := capture.ObserveBase(h.predecessor.Address()); err != nil {
			return nil, err
		}
	}
	// Candidate observations use the private predecessor; journal fences
	// retain committed roots, including names already changed by this TX.
	roleRevision := stage.RoleRevision
	if changes.RolesBase != nil {
		roleRevision = changes.RolesBase.Revision
	}
	for _, observed := range namespaces {
		changes.AddFence(extensionAuthorityFence{observation: extension.AuthorityObservation{Roles: roleRevision, NamespaceAddress: observed.Address, Namespace: observed.Predecessor, Database: stage.DatabasePredecessor}})
	}
	stage.FinishAuthority = &session.CatalogAuthority{Session: scope.Op.Session, Authorization: stage.AuthRevision, Roles: stage.RoleRevision, Ref: ids.ObjectRef{Address: e.Catalog.Owner.DatabaseAddress()}, Descriptor: stage.DatabasePredecessor, DatabaseAuthority: stage.DatabaseAuthority, DatabasePredecessor: stage.DatabasePredecessor}
	return changes, nil
}

// stageExtensionLocked stages CREATE EXTENSION under Engine.Mu. Any nonnil
// stage is transferred to the caller even on error: publish it on success or
// discard it after unlocking on failure. Pre-stage failures may return nil.
func stageExtensionLocked(e *core.Engine, scope *core.StatementScope, s *parser.CreateExtension) (*core.CatalogStage, error) {
	auth, err := e.AuthorizationLocked(scope.Op)
	if err != nil {
		return nil, err
	}
	host := &extensionHost{Engine: e, Op: scope.Op, Auth: auth}
	staged, err := extension.StageCreate(host, auth, auth.View, s)
	if err != nil {
		return host.Stage, err
	}
	if staged == nil {
		return nil, nil
	}
	stage, err := stageOf(staged)
	if err == nil {
		err = e.MaterializeHostConversions(stage)
	}
	if err == nil {
		ddl.RecordCommand(stage, "CREATE EXTENSION", stage.LookupName(ids.NameKey{ClassID: ids.ExtensionClassID, Name: s.Name.Text}))
	}
	return stage, err
}
