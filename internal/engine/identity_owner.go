package engine

// txOwnerOf recovers the transaction owner a ref is attached to, or nil.
// Engine code compares and dereferences attachments only through it.
func txOwnerOf(ref TxRef) *transactionOwner {
	owner, _ := ref.Attachment().(*transactionOwner)
	return owner
}

// privateSessionOf recovers the maintenance session a private token names,
// or nil for a public or zero token.
func privateSessionOf(token SessionToken) *Session {
	session, _ := token.Private().(*Session)
	return session
}
