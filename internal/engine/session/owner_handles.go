package session

import "stuntgres/internal/engine/ids"

// TxOwnerOf recovers the transaction owner a ref is attached to, or nil.
// Engine code compares and dereferences attachments only through it.
func TxOwnerOf(ref ids.TxRef) *TransactionOwner {
	owner, _ := ref.Attachment().(*TransactionOwner)
	return owner
}

// TokenAttached reports whether token is public, or private and names the
// maintenance session that owns it. Lock registration requires it.
func TokenAttached(token ids.SessionToken) bool {
	private := PrivateSessionOf(token)
	if token.ID() == 0 {
		return private != nil && private.SessionToken == token
	}
	return private == nil
}

// TxOwnerAttached reports whether ref carries its transaction owner, with a
// private session's owner belonging to the session the token names.
func TxOwnerAttached(ref ids.TxRef) bool {
	owner := TxOwnerOf(ref)
	return owner != nil && TokenAttached(ref.Session()) &&
		(ref.Session().ID() != 0 || owner.Session == PrivateSessionOf(ref.Session()))
}

// PrivateSessionOf recovers the maintenance session a private token names,
// or nil for a public or zero token.
func PrivateSessionOf(token ids.SessionToken) *Session {
	session, _ := token.Private().(*Session)
	return session
}
