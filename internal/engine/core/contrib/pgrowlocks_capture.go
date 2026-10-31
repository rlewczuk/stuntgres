package contrib

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Pgrowlocks materializes once, with no manager calls under Engine.mu. The
// relation lock is an exact, temporary contribution rather than SELECT's normal
// transaction-lifetime AccessShare grant.
func Pgrowlocks[A Authority](h Host[A], text string, result *sqltype.Descriptor) (_ []sqltype.Value, err error) {
	// These are runtime extension diagnostics, not SQL argument occurrences.
	defer func() {
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Position != "" {
			copy := *diagnostic
			copy.Position = ""
			err = &copy
		}
	}()
	if err := h.Active(); err != nil {
		return nil, err
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return nil, sqlError("42602", "invalid name syntax")
	}
	name, err = regobj.ClassQualifiedName(name, text, h.Operation().Session.DatabaseName)
	if err != nil {
		return nil, err
	}
	h.ReacquireEngine()
	auth, err := h.CallableAuthorizationLocked()
	var ref ids.ObjectRef
	var descriptor *catalog.Relation
	var stored *storage.Relation
	if err == nil {
		ref, err = h.ResolveRelationName(auth, name)
	}
	if err == nil {
		descriptor, _ = auth.Access().Catalog.Relation(ref.Address)
		stored = h.PrivateStorageLocked(ref.Address)
	}
	h.ReleaseEngineLocked()
	if err != nil {
		return nil, err
	}

	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.AccessShare, &contribution); err != nil {
		return nil, err
	}

	h.ReacquireOwnerEngine()
	auth, err = h.CallableAuthorizationLocked()
	if err == nil {
		err = h.PgrowlocksReadmitLocked(auth, name, ref, descriptor, stored)
	}
	if err == nil {
		err = exec.CheckPgrowlocksTable(auth.Access(), auth, ref, descriptor)
	}
	var view *visibility.View
	local := false
	if err == nil {
		view, local, err = h.RelationDataViewLocked(ref)
	}
	h.ReleaseOwnerEngineLocked()
	if err != nil {
		return nil, err
	}
	if local {
		defer view.Release()
	}
	tuples, err := exec.PgrowlocksPrefix(h.Operation().Context(), view, ref)
	if err != nil {
		return nil, err
	}
	records, err := h.LockManager().Pgrowlocks(h.Operation().Context(), h.Ticket(), tuples)
	if err != nil {
		return nil, err
	}
	h.ReacquireEngine()
	valid := h.CatalogEpochLocked() == ref.Epoch &&
		h.PrivateCatalogLocked(h.PendingCatalog()).Get(ref.Address) == descriptor &&
		h.PrivateStorageLocked(ref.Address) == stored
	h.ReleaseEngineLocked()
	if !valid {
		return nil, ids.ErrCatalogReplaced
	}
	return exec.PgrowlocksValues(h.Operation().Context(), result, records)
}
