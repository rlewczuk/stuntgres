package lock

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
)

// lockFixture builds lock manager state without an engine: one identity
// domain, one catalog epoch, numbered public sessions, and registered tickets
// whose transaction owners are opaque markers.
type lockFixture struct {
	t      *testing.T
	domain *ids.Domain
	m      *Manager
	epoch  ids.CatalogEpoch
	next   uint64 // last session, transaction or object number handed out
}

type lockFixtureOwner struct{ ids.TxOwnerMarker }

func newLockFixture(t *testing.T) *lockFixture {
	domain := new(ids.Domain)
	return &lockFixture{t: t, domain: domain, m: New(domain), epoch: 1}
}

// session mints a new public session identity.
func (f *lockFixture) session() ids.SessionToken {
	f.next++
	return f.domain.Session(ids.SessionID(f.next))
}

// tx registers a transaction ticket for a new session. The ticket ends when
// the test does.
func (f *lockFixture) tx() *Ticket { return f.txIn(f.session()) }

// txIn registers a further transaction ticket of session.
func (f *lockFixture) txIn(session ids.SessionToken) *Ticket {
	f.t.Helper()
	f.next++
	ref := f.domain.Tx(session, ids.TxID(f.next), f.epoch, &lockFixtureOwner{}, uint32(f.next))
	ticket, err := f.m.Register(ref)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { f.m.End(ticket) })
	return ticket
}

// advisory registers session's persistent advisory ticket.
func (f *lockFixture) advisory(session ids.SessionToken) *Ticket {
	f.t.Helper()
	ticket, err := f.m.RegisterSession(session)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { f.m.End(ticket) })
	return ticket
}

// relation is a new relation's resource in the fixture's epoch.
func (f *lockFixture) relation() Resource {
	f.next++
	return Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(16384 + f.next)})}
}

// typeResource is a new type's resource in the fixture's epoch.
func (f *lockFixture) typeResource() Resource {
	f.next++
	return Resource{ObjectRef: f.domain.Ref(f.epoch, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(16384 + f.next)})}
}

// request acquires with a bounded context: nil when granted at once,
// otherwise the queued request.
func (f *lockFixture) request(ticket *Ticket, resource Resource, mode Mode) *relationRequest {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	f.t.Cleanup(cancel)
	r, err := f.m.StartAcquire(ctx, ticket, resource, mode, Policy{CompletionPurpose: CompletionVirtual})
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

// result waits, bounded, for a request's outcome.
func (f *lockFixture) result(r *relationRequest) error {
	f.t.Helper()
	if r == nil {
		return nil
	}
	select {
	case <-r.done:
		return r.err
	case <-time.After(time.Second):
		f.t.Fatal("request did not terminate before failure guard")
		return nil
	}
}

// empty fails unless no ticket is registered and no resource is tracked.
func (f *lockFixture) empty() {
	f.t.Helper()
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	if len(f.m.owners) != 0 || len(f.m.entries) != 0 {
		f.t.Fatalf("residual lock state: owners=%d entries=%d", len(f.m.owners), len(f.m.entries))
	}
}
