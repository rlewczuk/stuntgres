package core

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
)

// The EXPLAIN renderer's host is the statement host; AuthorizationView is its
// authority. Context, Operation, Environments, AdmittedPlannerMethods and
// ReleaseReplacementRead are shared with exec.Host.
var _ Host = (*statementHost)(nil)
var _ Authority = AuthorizationView{}

func (h *statementHost) PendingCatalog() *session.Journal { return h.scope().PendingCatalog() }
func (h *statementHost) CatalogDomain() *ids.Domain       { return h.scope().Engine().Catalog.Domain }

func (h *statementHost) PartitionCatalog(current catalog.Read) catalog.Read {
	s := h.scope()
	return partition.CaptureCatalog(s.inheritanceRead(current), s.PartitionRelations, s.PendingDetachSnapshot())
}

func (h *statementHost) EngineStorageLocked() *storage.Store { return &h.scope().Engine().Storage }

func (h *statementHost) PrivateStorageLocked(address ids.ObjectAddress) *storage.Relation {
	s := h.scope()
	return s.Engine().PrivateStorageLocked(s.PendingCatalog(), address)
}

func (h *statementHost) ImplicitStatement() bool { return h.implicit && h.Catalog == nil }

func (h *statementHost) CheckRestoreEffect() error { return CheckRestoreEffect(h.Op) }

func (h *statementHost) CurrentAccessLocked() (access.View, error) {
	current, err := h.scope().Engine().AuthorizationLocked(h.Op)
	return current.View, err
}

func (h *statementHost) AuthorizeViewSourceLocked(ref ids.ObjectRef, definition *stored.View, invoker authorization.PrincipalRef, mark plan.RowMark, current authorization.PrincipalRef) (Authority, error) {
	s := h.scope()
	auth, err := s.Engine().authorizeViewSourceLocked(s, ref, definition, invoker, mark)
	if err != nil {
		return nil, err
	}
	auth.State.CurrentUser = current
	return auth, nil
}

// ReleaseEngineLocked and ReacquireEngine bracket the renderer's
// provider-callback and detached-comparison windows: Engine.Mu is released
// for the callback and held again before presentation continues.
func (h *statementHost) ReleaseEngineLocked() { h.scope().Engine().Mu.Unlock() }
func (h *statementHost) ReacquireEngine()     { h.scope().Engine().Mu.Lock() }
