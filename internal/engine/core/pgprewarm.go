package core

import "stuntgres/internal/engine/ids"

// PrewarmBlockLocked borrows the executing owner and Engine.Mu. Admission uses
// precisely the ordinary committed-incarnation fence; private/TEMP captures do
// not become shared entries. The leaf owns no borrowed permission or callback.
func (h *statementHost) PrewarmBlockLocked(ref ids.ObjectRef, fork int16, block int64) error {
	scope := h.scope()
	e := scope.Engine()
	if err := scope.Op.Context().Err(); err != nil { return err }
	if e.Cluster == nil { return ids.ErrCatalogReplaced }
	view := e.Catalog.Current()
	database, ok := view.Database(e.Catalog.DatabaseAddress())
	if !ok || e.Cluster.Targets[database.Name()] != e { return ids.ErrCatalogReplaced }
	key, eligible := e.logicalBufferReadKeyLocked(scope, h.PrivateCatalogLocked(scope.PendingCatalog()), ref.Address)
	if !eligible { return nil }
	if key.epoch != ref.Epoch || key.domain != ref.Domain() { return ids.ErrCatalogReplaced }
	key.fork, key.block = fork, block
	cache := e.logicalBuffersLocked()
	entry, _ := cache.open(key, scope.Op.Session.SessionToken)
	cache.close(entry, scope.Op.Session.SessionToken)
	return nil
}
