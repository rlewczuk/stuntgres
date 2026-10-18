package engine

import "stuntgres/internal/engine/ids"

// Tests forge stale or foreign identities by changing one component of a
// valid value; these helpers rebuild the value through its constructor.

func withTxID(ref TxRef, id TxID) TxRef {
	return ref.Domain().Tx(ref.Session(), id, ref.Epoch(), ref.Attachment(), ref.Diagnostic())
}

func withTxSession(ref TxRef, session ids.SessionToken) TxRef {
	return ref.Domain().Tx(session, ref.ID(), ref.Epoch(), ref.Attachment(), ref.Diagnostic())
}

// withSessionID rebuilds a public token with another ID.
func withSessionID(token ids.SessionToken, id ids.SessionID) ids.SessionToken {
	return token.Domain().Session(id)
}

func withSessionDomain(token ids.SessionToken, domain *ids.Domain) ids.SessionToken {
	if private := token.Private(); private != nil {
		return domain.PrivateSession(private)
	}
	return domain.Session(token.ID())
}
