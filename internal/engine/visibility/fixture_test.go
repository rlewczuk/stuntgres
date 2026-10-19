package visibility

import (
	"errors"
	"sync"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// fixture builds registry state without an engine: one identity domain, one
// catalog epoch and numbered public sessions. mu stands in for Engine.mu.
type fixture struct {
	t      *testing.T
	domain *ids.Domain
	epoch  ids.CatalogEpoch
	mu     sync.Mutex
	reg    Registry
	next   uint64 // last session number handed out
}

type fixtureOwner struct{ ids.TxOwnerMarker }

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, domain: new(ids.Domain), epoch: 1, reg: NewRegistry(false)}
}

// tx mints the first transaction of a new public session. Like the engine,
// every session numbers its transactions from 1, so only the session and the
// diagnostic identity tell two incarnations apart.
func (f *fixture) tx() ids.TxRef {
	f.next++
	session := f.domain.Session(ids.SessionID(f.next))
	return f.domain.Tx(session, 1, f.epoch, &fixtureOwner{}, uint32(f.next))
}

// node enrolls a new serializable transaction and captures its snapshot at the
// current frontier and finalization order.
func (f *fixture) node(readOnly bool) *SerializableTx {
	f.t.Helper()
	ref := f.tx()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reg.EnrollSerializableLocked(ref, true)
	node := f.reg.Serializable.Nodes[ref]
	node.Captured = true
	node.ReadOnly = readOnly
	node.Frontier = f.reg.CommitSeq()
	node.SnapshotOrder = f.reg.SerializableOrder()
	return node
}

func serializableUnitDiagnostic(t *testing.T, err error, detail string) {
	t.Helper()
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to read/write dependencies among transactions", Detail: "Reason code: " + detail, Hint: "The transaction might succeed if retried."}
	if !errors.As(err, &got) || *got != *want {
		t.Fatalf("diagnostic got %#v (%v), want %#v", got, err, want)
	}
}
