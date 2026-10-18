package engine

// Ref mints an object reference in this domain. A nil domain yields a
// reference without provenance, which every consumer rejects.
func (d *Domain) Ref(epoch CatalogEpoch, address ObjectAddress) ObjectRef {
	return ObjectRef{owner: d, Epoch: epoch, Address: address}
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

func (r ObjectRef) Domain() *Domain { return r.owner }

func (t SessionToken) Domain() *Domain       { return t.domain }
func (t SessionToken) Private() SessionOwner { return t.private }

func (r TxRef) Domain() *Domain       { return r.domain }
func (r TxRef) Session() SessionToken { return r.session }
func (r TxRef) Epoch() CatalogEpoch   { return r.epoch }
func (r TxRef) Attachment() TxOwner   { return r.attachment }
func (r TxRef) Diagnostic() uint32    { return r.diagnostic }
