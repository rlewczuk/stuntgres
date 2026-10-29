package core

import (
	"context"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Cluster-shared identities reserve beyond every database's frontier. The
// cluster mutex is already held by staging; ordinary private reservations
// advance the same frontier as other catalog allocations.
func (h DDLHost) AllocateSharedOID() (ids.OID, error) {
	oid, err := catalog.DatabaseOIDLocked(h.E.Catalog.NextOID(), &h.E.Catalog, h.E.Cluster.Catalogs)
	if err != nil {
		return 0, err
	}
	for store := range h.E.Cluster.Catalogs {
		store.InstallOIDFrontierLocked(max(store.NextOID(), oid+1))
	}
	h.E.Catalog.InstallOIDFrontierLocked(max(h.E.Catalog.NextOID(), oid+1))
	return oid, nil
}
func (h DDLHost) SubscriptionInBlock() bool { return h.Op.Session.Transactions.InBlock() }

// Portable replay creates registrations; replacement remints both the catalog
// address and the allocated main origin against retained peers and live claims.
func (e *Engine) remintSubscriptionsLocked(ctx context.Context, next *catalog.State, oids *catalog.OIDReservation) error {
	hasSubscriptions := false
	for range e.Catalog.Read(next).Subscriptions() {
		hasSubscriptions = true
		break
	}
	if !hasSubscriptions {
		return nil
	}
	floor := oids.Next
	for address := range next.Descriptors() {
		if address.ObjectID >= floor {
			floor = address.ObjectID + 1
			if floor == 0 {
				return catalog.Conflict{}
			}
		}
	}
	occupancy := e.replicationOriginOccupancyLocked(true)
	addresses := make([]ids.ObjectAddress, 0)
	for address := range e.Catalog.Read(next).Subscriptions() {
		addresses = append(addresses, address)
	}
	slices.SortFunc(addresses, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID { return -1 }
		if a.ObjectID > b.ObjectID { return 1 }
		return 0
	})
	for _, address := range addresses {
		d := e.Catalog.Read(next).Get(address).(*catalog.Subscription)
		oid, err := catalog.DatabaseOIDLocked(floor, &e.Catalog, e.Cluster.Catalogs)
		if err != nil {
			return err
		}
		n := *d
		n.ObjectAddress.ObjectID = oid
		n.Database = e.Catalog.DatabaseAddress()
		n.ReplicationOriginID, err = occupancy.allocate(ctx)
		if err != nil { return err }
		next.UnbindName(catalog.DescriptorNameKey(d))
		next.RemoveDescriptor(address)
		next.SetDescriptor(n.Address(), &n)
		next.BindName(catalog.DescriptorNameKey(&n), n.Address())
		floor = oid + 1
		if floor == 0 {
			return catalog.Conflict{}
		}
		oids.Next = max(oids.Next, floor)
	}
	return nil
}

func (h DDLHost) ValidateSubscriptionSync(value string) error {
	definition, err := h.E.Settings.Lookup("synchronous_commit")
	if err != nil {
		return err
	}
	_, _, err = definition.Normalize(value)
	return err
}

func (h DDLHost) ParseSubscriptionLSN(text string) (uint64, error) {
	typ := h.StageRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 3220}).SQLType
	value, err := typ.Input(h.Context(), text, -1)
	if err != nil {
		return 0, err
	}
	return value.PGLSN()
}
