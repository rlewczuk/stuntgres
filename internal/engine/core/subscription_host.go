package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Subscription identities share every database's allocation frontier. The
// cluster mutex is already held by DDL staging; private reservations advance
// the same frontier as other ordinary catalog allocations.
func (h DDLHost) AllocateSubscriptionOID() (ids.OID, error) {
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

// Replacement subscriptions have no embedded domain refs or consumers to
// rewrite. Remint them before retaining peer databases, so even an imported
// address equal to a peer's subscription cannot overwrite that definition.
func (e *Engine) remintSubscriptionsLocked(next *catalog.State, oids *catalog.OIDReservation) error {
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
	for address, d := range e.Catalog.Read(next).Subscriptions() {
		oid, err := catalog.DatabaseOIDLocked(floor, &e.Catalog, e.Cluster.Catalogs)
		if err != nil {
			return err
		}
		n := *d
		n.ObjectAddress.ObjectID = oid
		n.Database = e.Catalog.DatabaseAddress()
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
