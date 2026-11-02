package ids

type SessionID uint64

// SessionToken names registered or private session authority, or detached
// catalog metadata. A detached token has a display ID but fresh opaque identity;
// it never equals a registered session, even at the same domain and numeric ID.
type SessionToken struct {
	domain  *Domain
	id      SessionID
	private SessionOwner
	detached *detachedSession
}

func (t SessionToken) Domain() *Domain       { return t.domain }
func (t SessionToken) ID() SessionID         { return t.id }
func (t SessionToken) Private() SessionOwner { return t.private }
func (t SessionToken) IsDetached() bool      { return t.detached != nil }

type detachedSession struct{ identity byte }

type TxID uint64
type CommandID uint64
type CommitSeq uint64
type RowID uint64
type VersionID uint64

// TxRef names one transaction of one session, within a domain and catalog
// epoch, attached to its engine-side owner.
type TxRef struct {
	domain     *Domain
	session    SessionToken
	id         TxID
	epoch      CatalogEpoch
	attachment TxOwner
	diagnostic uint32
}

// CommandIdentity names one command of one transaction. Prepared domain
// checks and partition routines record it as the phase they were admitted in.
type CommandIdentity struct {
	Transaction TxRef
	Command     CommandID
}

func (r TxRef) Domain() *Domain       { return r.domain }
func (r TxRef) Session() SessionToken { return r.session }
func (r TxRef) ID() TxID              { return r.id }
func (r TxRef) Epoch() CatalogEpoch   { return r.epoch }
func (r TxRef) Attachment() TxOwner   { return r.attachment }
func (r TxRef) Diagnostic() uint32    { return r.diagnostic }

// TxCompletion is a transaction identity's completion state.
type TxCompletion uint32

const (
	TxInProgress TxCompletion = iota
	TxCommitted
	TxAborted
)
