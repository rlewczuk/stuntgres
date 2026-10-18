package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestAdvisorySessionResourceDomains(t *testing.T) {
	e, other := New(), New()
	a, ra := advisoryUnitOwner(t, e)
	b, rb := advisoryUnitOwner(t, e)
	isolated, ri := advisoryUnitOwner(t, other)
	boundaries := [][2]uint32{{0, 0}, {0x80000000, 0}, {0x7fffffff, 0xffffffff}, {0xffffffff, 0xffffffff}}
	for _, words := range boundaries {
		for _, kind := range []lockResourceKind{advisoryBigintResource, advisoryPairResource} {
			key := lockResource{Kind: kind, High: words[0], Low: words[1]}
			advisoryUnitGrant(t, a, ra, key, advisoryExclusive)
			advisoryUnitGrant(t, isolated, ri, key, advisoryExclusive)
			request, err := advisoryUnitAcquire(t, b, rb, key, advisoryExclusive, true)
			if request != nil || !errors.Is(err, errRelationUnavailable) {
				t.Fatal("busy try created/succeeded request", request, err)
			}
			lockState := e.locks.Inspect(b.advisory.ticket, b.transactions.ticket)
			pending := len(lockState.Resources[key].Pending)
			persistentJournal := len(lockState.Journals[b.advisory.ticket])
			transientJournal := len(lockState.Journals[b.transactions.ticket])
			unallocated := lockState.Journals[b.advisory.ticket] == nil
			if pending != 0 || persistentJournal != 0 || transientJournal != 0 || !unallocated {
				t.Fatal("false try allocated/journaled ownership", pending, persistentJournal, transientJournal, unallocated)
			}
			if advisoryUnitHold(a, key).Exclusive != 1 || advisoryUnitHold(isolated, key).Exclusive != 1 {
				t.Fatal("key/domain isolation")
			}
		}
	}
	key := lockResource{Kind: advisoryBigintResource, High: 0xffffffff, Low: 0xffffffff}
	forged := *a.advisory.ticket
	if _, err := e.locks.StartAcquire(context.Background(), &forged, key, advisoryExclusive, relationLockPolicy{Provenance: ra, NoWait: true}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("copied ticket authorized", err)
	}
	if _, err := other.locks.StartAcquire(context.Background(), a.advisory.ticket, key, advisoryExclusive, relationLockPolicy{Provenance: ra, NoWait: true}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("foreign manager authorized", err)
	}
	contaminated := key
	contaminated.ObjectRef = ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1}}
	if _, err := advisoryUnitAcquire(t, a, ra, contaminated, advisoryExclusive, true); err == nil || errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("fabricated catalog identity accepted for advisory", err)
	}
	retiredSource := ra
	retiredSource = withTxID(retiredSource, retiredSource.ID()+1)
	if _, err := advisoryUnitAcquire(t, a, retiredSource, key, advisoryExclusive, true); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("missing exact transient provenance authorized", err)
	}
	if advisoryUnitHold(a, key).Exclusive != 1 || advisoryUnitHold(isolated, key).Exclusive != 1 {
		t.Fatal("invalid request mutated registered counts")
	}
}
