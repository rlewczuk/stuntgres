// Package ids holds the engine's identity values: catalog object identities,
// runtime session and transaction identities, and the per-engine provenance
// Domain that mints them. It has no behavior; the engine owns every check.
package ids

// Domain is one engine's provenance token. It has nonzero size, so distinct
// live engines have distinct pointer identities. Use it only as *Domain; a nil
// domain is invalid. The engine allocates one per instance and keeps it across
// Reset and Restore.
type Domain struct{ identity byte }

// Ref mints an object reference in this domain. A nil domain yields a
// reference without provenance, which every consumer rejects.
func (d *Domain) Ref(epoch CatalogEpoch, address ObjectAddress) ObjectRef {
	return ObjectRef{domain: d, Epoch: epoch, Address: address}
}

// Session mints the token of a registered session.
func (d *Domain) Session(id SessionID) SessionToken {
	return SessionToken{domain: d, id: id}
}

// PrivateSession mints the token of a private maintenance session. It has no
// public ID; the owner's pointer identity distinguishes it. Pass a non-nil
// owner: a typed nil pointer would not compare equal to the zero token.
func (d *Domain) PrivateSession(owner SessionOwner) SessionToken {
	return SessionToken{domain: d, private: owner}
}

// Tx mints a transaction reference. Pass a nil interface, not a typed nil
// pointer, for an unattached reference.
func (d *Domain) Tx(session SessionToken, id TxID, epoch CatalogEpoch, owner TxOwner, diagnostic uint32) TxRef {
	return TxRef{domain: d, session: session, id: id, epoch: epoch, attachment: owner, diagnostic: diagnostic}
}

// TxOwner is the engine-side owner a TxRef is attached to. Only types that
// embed TxOwnerMarker implement it, so a TxRef holds no foreign values, and
// equality stays pointer identity of the owner.
type TxOwner interface{ txOwner() }

// TxOwnerMarker is embedded in the transaction owner to implement TxOwner.
type TxOwnerMarker struct{}

func (*TxOwnerMarker) txOwner() {}

// SessionOwner is the session a private maintenance SessionToken names. Only
// types that embed SessionOwnerMarker implement it.
type SessionOwner interface{ sessionOwner() }

// SessionOwnerMarker is embedded in the session to implement SessionOwner.
type SessionOwnerMarker struct{}

func (*SessionOwnerMarker) sessionOwner() {}
