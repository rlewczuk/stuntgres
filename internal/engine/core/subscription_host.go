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
	floor := oids.Next
	for address := range next.Descriptors() {
		if address.ClassID != ids.ReplicationOriginClassID && address.ObjectID >= floor {
			floor = address.ObjectID + 1
			if floor == 0 {
				return catalog.Conflict{}
			}
		}
	}
	occupancy := e.replicationOriginOccupancyLocked(true)
	origins := make([]*catalog.ReplicationOrigin, 0)
	for _, d := range e.Catalog.Read(next).ReplicationOrigins() {
		origins = append(origins, d)
	}
	slices.SortFunc(origins, func(a, b *catalog.ReplicationOrigin) int {
		if a.Name() < b.Name() {
			return -1
		}
		if a.Name() > b.Name() {
			return 1
		}
		return 0
	})
	for _, d := range origins {
		next.UnbindName(catalog.DescriptorNameKey(d))
		next.RemoveDescriptor(d.Address())
	}
	subscriptions := make(map[ids.ObjectAddress]*catalog.Subscription)
	addresses := make([]ids.ObjectAddress, 0)
	for address := range e.Catalog.Read(next).Subscriptions() {
		addresses = append(addresses, address)
	}
	slices.SortFunc(addresses, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
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
		next.UnbindName(catalog.DescriptorNameKey(d))
		next.RemoveDescriptor(address)
		subscriptions[address] = &n
		floor = oid + 1
		if floor == 0 {
			return catalog.Conflict{}
		}
		oids.Next = max(oids.Next, floor)
	}
	for _, d := range origins {
		id, err := occupancy.allocate(ctx)
		if err != nil {
			return err
		}
		n := *d
		n.ObjectAddress.ObjectID = id
		if subscription := subscriptions[d.Subscription]; subscription != nil {
			n.Subscription = subscription.Address()
			n.ObjectName = subscription.ReplicationOriginName()
			subscription.ReplicationOriginID = id
		}
		next.SetDescriptor(n.Address(), &n)
		next.BindName(catalog.DescriptorNameKey(&n), n.Address())
	}
	for _, d := range subscriptions {
		next.SetDescriptor(d.Address(), d)
		next.BindName(catalog.DescriptorNameKey(d), d.Address())
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
