package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Imported parameters have no address-bearing consumers: native replay selects
// them by canonical name. Reserve destination identities privately, alongside
// subscription reminting, and publish the frontier only with replacement.
func (e *Engine) remintParametersLocked(next *catalog.State, oids *catalog.OIDReservation) error {
	floor := oids.Next
	hasParameters := false
	for _, descriptor := range next.Descriptors() {
		if _, ok := descriptor.(*catalog.Parameter); ok {
			hasParameters = true
			break
		}
	}
	if !hasParameters { return nil }
	// The destination's Base and root fence already cover the sole-store case,
	// including registration (which projects a new database descriptor there).
	if e.Cluster != nil && len(e.Cluster.Targets) > 1 {
		oids.PeerFrontiers = make(map[*catalog.Store]ids.OID, len(e.Cluster.Targets)-1)
		for store := range e.Cluster.Catalogs {
			if store != &e.Catalog {
				oids.PeerFrontiers[store] = store.NextOID()
			}
		}
	}
	for address := range next.Descriptors() {
		if address.ObjectID >= floor {
			floor = address.ObjectID + 1
			if floor == 0 { return ids.ErrCatalogReplaced }
		}
	}
	for _, descriptor := range next.Descriptors() {
		parameter, ok := descriptor.(*catalog.Parameter)
		if !ok { continue }
		oid, err := catalog.DatabaseOIDLocked(floor, &e.Catalog, e.Cluster.Catalogs)
		if err != nil { return err }
		n := *parameter
		n.ObjectAddress.ObjectID = oid
		next.UnbindName(catalog.DescriptorNameKey(parameter))
		next.RemoveDescriptor(parameter.Address())
		next.SetDescriptor(n.Address(), &n)
		next.BindName(catalog.DescriptorNameKey(&n), n.Address())
		floor = oid + 1
		if floor == 0 { return ids.ErrCatalogReplaced }
		oids.Next = max(oids.Next, floor)
	}
	return nil
}

// Shared reservations must survive unlocked provider preparation. A peer-local
// allocation need not change this database's root or frontier, so validate both
// the registered store set and each original frontier before installation.
func (e *Engine) sharedOIDReservationCurrentLocked(oids catalog.OIDReservation) bool {
	if oids.PeerFrontiers == nil { return true }
	count := 0
	for store := range e.Cluster.Catalogs {
		if store == &e.Catalog { continue }
		original, captured := oids.PeerFrontiers[store]
		if !captured || store.NextOID() != original { return false }
		count++
	}
	return count == len(oids.PeerFrontiers)
}
