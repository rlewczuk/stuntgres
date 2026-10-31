package core

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
)

// VerifyHeapam pins exact regclass/storage authority, borrows a temporary
// AccessShare contribution, and scans retained unfiltered roots outside locks.
func VerifyHeapam[A Authority](h Host[A], args [6]sqltype.Value, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := h.Active(); err != nil {
		return nil, err
	}
	options, err := exec.VerifyHeapamOptions(args)
	if err != nil {
		return nil, err
	}
	h.ReacquireEngine()
	auth, err := h.WriteAuthorizationLocked()
	var ref ids.ObjectRef
	var descriptor *catalog.Relation
	var stored *storage.Relation
	var sequence *storage.SequenceGenerator
	if err == nil {
		ref = h.CatalogDomain().Ref(auth.Access().Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: options.Relation})
		descriptor, _ = auth.Access().Catalog.Relation(ref.Address)
		if descriptor == nil {
			err = sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", options.Relation))
		} else {
			stored = h.JournalStorageLocked(h.OwnerCatalog(), ref.Address)
			if descriptor.RelationKind == catalog.SequenceRelation {
				sequence = h.SequenceGeneratorLocked(ref.Address)
			}
		}
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
	auth, err = h.WriteAuthorizationLocked()
	if err == nil {
		err = h.VerifyHeapamReadmitLocked(auth, ref, descriptor, stored, sequence)
	}
	if err == nil {
		err = exec.CheckVerifyHeapamTarget(auth.Access().Catalog, descriptor, options)
	}
	if err == nil {
		h.MarkTemporaryTargetLocked(ref)
		err = access.CheckTemporaryStorageAccess(auth.Access().Catalog, descriptor, h.Operation().Session.SessionToken)
	}
	var view *visibility.View
	local := false
	if err == nil && descriptor.RelationKind != catalog.SequenceRelation {
		if stored == nil {
			err = fmt.Errorf("stuntgres: heap verification storage missing")
		} else {
			view, local, err = h.RelationDataViewLocked(ref)
		}
	} else if err == nil && sequence == nil {
		err = fmt.Errorf("stuntgres: sequence state missing")
	}
	h.ReleaseOwnerEngineLocked()
	if local && view != nil {
		defer view.Release()
	}
	if err != nil {
		return nil, err
	}
	var findings []storage.HeapFinding
	if view != nil {
		findings, err = exec.VerifyHeapamPrefix(h.Operation().Context(), view, ref, descriptor, options.Stop)
		if err != nil {
			return nil, err
		}
	}
	rows, err := exec.VerifyHeapamValues(h.Operation().Context(), result, findings)
	if err != nil {
		return nil, err
	}
	if err := h.Operation().Context().Err(); err != nil {
		return nil, err
	}
	h.ReacquireEngine()
	valid := h.CatalogEpochLocked() == ref.Epoch && h.PrivateCatalogLocked(h.OwnerCatalog()).Get(ref.Address) == descriptor && h.JournalStorageLocked(h.OwnerCatalog(), ref.Address) == stored
	if descriptor.RelationKind == catalog.SequenceRelation {
		valid = valid && h.SequenceGeneratorLocked(ref.Address) == sequence
	}
	h.ReleaseEngineLocked()
	if !valid {
		return nil, ids.ErrCatalogReplaced
	}
	return rows, nil
}
