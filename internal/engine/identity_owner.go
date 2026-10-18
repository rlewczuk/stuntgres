package engine

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

// SessionOwnerMarker is embedded in Session to implement SessionOwner.
type SessionOwnerMarker struct{}

func (*SessionOwnerMarker) sessionOwner() {}

// txOwnerOf recovers the transaction owner a ref is attached to, or nil.
// Engine code compares and dereferences attachments only through it.
func txOwnerOf(ref TxRef) *transactionOwner {
	owner, _ := ref.attachment.(*transactionOwner)
	return owner
}

// privateSessionOf recovers the maintenance session a private token names,
// or nil for a public or zero token.
func privateSessionOf(token SessionToken) *Session {
	session, _ := token.private.(*Session)
	return session
}
