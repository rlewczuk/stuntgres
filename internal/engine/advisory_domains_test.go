package engine

import (
	"context"
	"errors"
	"testing"
)

func TestAdvisorySessionResourceDomains(t *testing.T) {
	e, other := New(), New()
	a, ra := advisoryUnitOwner(t, e)
	b, rb := advisoryUnitOwner(t, e)
	isolated, ri := advisoryUnitOwner(t, other)
	boundaries := [][2]uint32{{0, 0}, {0x80000000, 0}, {0x7fffffff, 0xffffffff}, {0xffffffff, 0xffffffff}}
	for _, words := range boundaries {
		for _, kind := range []lockResourceKind{advisoryBigintResource, advisoryPairResource} {
			key := lockResource{kind: kind, high: words[0], low: words[1]}
			advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
			advisoryUnitGrant(t, isolated, ri, key, advisoryExclusive)
			request, err := advisoryUnitAcquire(t, b, rb, key, advisoryExclusive, true)
			if request != nil || !errors.Is(err, errRelationUnavailable) {
				t.Fatal("busy try created/succeeded request", request, err)
			}
			e.locks.mu.Lock()
			pending := len(e.locks.entries[key].pending)
			persistentJournal := len(b.advisory.ticket.resources)
			transientJournal := len(b.transactions.ticket.resources)
			unallocated := b.advisory.ticket.resources == nil
			e.locks.mu.Unlock()
			if pending != 0 || persistentJournal != 0 || transientJournal != 0 || !unallocated {
				t.Fatal("false try allocated/journaled ownership", pending, persistentJournal, transientJournal, unallocated)
			}
			if advisoryUnitHold(a, key).exclusive != 1 || advisoryUnitHold(isolated, key).exclusive != 1 {
				t.Fatal("key/domain isolation")
			}
		}
	}
	key := lockResource{kind: advisoryBigintResource, high: 0xffffffff, low: 0xffffffff}
	forged := *a.advisory.ticket
	if _, err := e.locks.startAcquire(context.Background(), &forged, key, advisoryExclusive, relationLockPolicy{provenance: ra, nowait: true}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("copied ticket authorized", err)
	}
	if _, err := other.locks.startAcquire(context.Background(), a.advisory.ticket, key, advisoryExclusive, relationLockPolicy{provenance: ra, nowait: true}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("foreign manager authorized", err)
	}
	if _, err := e.locks.startAcquire(context.Background(), a.transactions.ticket, key, advisoryExclusive, relationLockPolicy{nowait: true}); err == nil || errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("advisory payload accepted by ordinary ticket", err)
	}
	contaminated := key
	contaminated.ObjectRef = ObjectRef{Address: ObjectAddress{ClassID: RelationClassID, ObjectID: 1}}
	if _, err := advisoryUnitAcquire(t, a, ra, contaminated, advisoryExclusive, true); err == nil || errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("fabricated catalog identity accepted for advisory", err)
	}
	retiredSource := ra
	retiredSource.id++
	if _, err := advisoryUnitAcquire(t, a, retiredSource, key, advisoryExclusive, true); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("missing exact transient provenance authorized", err)
	}
	if advisoryUnitHold(a, key).exclusive != 1 || advisoryUnitHold(isolated, key).exclusive != 1 {
		t.Fatal("invalid request mutated registered counts")
	}
}
