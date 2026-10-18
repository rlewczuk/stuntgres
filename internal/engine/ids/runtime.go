package ids

type SessionID uint64

// SessionToken names a session within one engine domain. A registered
// session has a nonzero ID and no private owner; a private maintenance
// session has ID zero and is named by its owner's pointer identity.
type SessionToken struct {
	domain  *Domain
	id      SessionID
	private SessionOwner
}

func (t SessionToken) Domain() *Domain       { return t.domain }
func (t SessionToken) ID() SessionID         { return t.id }
func (t SessionToken) Private() SessionOwner { return t.private }

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
