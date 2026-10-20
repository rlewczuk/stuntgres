package engine

import "stuntgres/internal/engine/ids"

// txOwnerOf recovers the transaction owner a ref is attached to, or nil.
// Engine code compares and dereferences attachments only through it.
func txOwnerOf(ref ids.TxRef) *transactionOwner {
	owner, _ := ref.Attachment().(*transactionOwner)
	return owner
}

// sessionTokenAttached reports whether token is public, or private and names
// the maintenance session that owns it. Lock registration requires it.
func sessionTokenAttached(token ids.SessionToken) bool {
	private := privateSessionOf(token)
	if token.ID() == 0 {
		return private != nil && private.token == token
	}
	return private == nil
}

// txOwnerAttached reports whether ref carries its transaction owner, with a
// private session's owner belonging to the session the token names.
func txOwnerAttached(ref ids.TxRef) bool {
	owner := txOwnerOf(ref)
	return owner != nil && sessionTokenAttached(ref.Session()) &&
		(ref.Session().ID() != 0 || owner.session == privateSessionOf(ref.Session()))
}

// privateSessionOf recovers the maintenance session a private token names,
// or nil for a public or zero token.
func privateSessionOf(token ids.SessionToken) *Session {
	session, _ := token.Private().(*Session)
	return session
}
