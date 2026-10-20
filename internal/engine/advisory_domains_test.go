package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

func TestAdvisorySessionResourceDomains(t *testing.T) {
	e, other := New(), New()
	a, ra := advisoryUnitOwner(t, e)
	b, rb := advisoryUnitOwner(t, e)
	isolated, ri := advisoryUnitOwner(t, other)
	boundaries := [][2]uint32{{0, 0}, {0x80000000, 0}, {0x7fffffff, 0xffffffff}, {0xffffffff, 0xffffffff}}
	for _, words := range boundaries {
		for _, kind := range []lock.ResourceKind{lock.AdvisoryBigintResource, lock.AdvisoryPairResource} {
			key := lock.Resource{Kind: kind, High: words[0], Low: words[1]}
			advisoryUnitGrant(t, a, ra, key, lock.AdvisoryExclusive)
			advisoryUnitGrant(t, isolated, ri, key, lock.AdvisoryExclusive)
			request, err := advisoryUnitAcquire(t, b, rb, key, lock.AdvisoryExclusive, true)
			if request != nil || !errors.Is(err, lock.ErrUnavailable) {
				t.Fatal("busy try created/succeeded request", request, err)
			}
			lockState := e.locks.Inspect(b.Advisory.ticket, b.Transactions.ticket)
			pending := len(lockState.Resources[key].Pending)
			persistentJournal := len(lockState.Journals[b.Advisory.ticket])
			transientJournal := len(lockState.Journals[b.Transactions.ticket])
			unallocated := lockState.Journals[b.Advisory.ticket] == nil
			if pending != 0 || persistentJournal != 0 || transientJournal != 0 || !unallocated {
				t.Fatal("false try allocated/journaled ownership", pending, persistentJournal, transientJournal, unallocated)
			}
			if advisoryUnitHold(a, key).Exclusive != 1 || advisoryUnitHold(isolated, key).Exclusive != 1 {
				t.Fatal("key/domain isolation")
			}
		}
	}
	key := lock.Resource{Kind: lock.AdvisoryBigintResource, High: 0xffffffff, Low: 0xffffffff}
	forged := *a.Advisory.ticket
	if _, err := e.locks.StartAcquire(context.Background(), &forged, key, lock.AdvisoryExclusive, lock.Policy{Provenance: ra, NoWait: true}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("copied ticket authorized", err)
	}
	if _, err := other.locks.StartAcquire(context.Background(), a.Advisory.ticket, key, lock.AdvisoryExclusive, lock.Policy{Provenance: ra, NoWait: true}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("foreign manager authorized", err)
	}
	contaminated := key
	contaminated.ObjectRef = ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1}}
	if _, err := advisoryUnitAcquire(t, a, ra, contaminated, lock.AdvisoryExclusive, true); err == nil || errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("fabricated catalog identity accepted for advisory", err)
	}
	retiredSource := ra
	retiredSource = withTxID(retiredSource, retiredSource.ID()+1)
	if _, err := advisoryUnitAcquire(t, a, retiredSource, key, lock.AdvisoryExclusive, true); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("missing exact transient provenance authorized", err)
	}
	if advisoryUnitHold(a, key).Exclusive != 1 || advisoryUnitHold(isolated, key).Exclusive != 1 {
		t.Fatal("invalid request mutated registered counts")
	}
}
