package lock

import (
	"context"
	"errors"
	"testing"
)

func scopedRelationGrant(t *testing.T, m *Manager, owner *Ticket, resource Resource, mode Mode, contribution *RelationContribution) {
	t.Helper()
	request, err := m.StartAcquireRelation(context.Background(), owner, resource, mode, Policy{}, contribution)
	if err != nil || request != nil {
		t.Fatal("unexpected scoped wait", request, err)
	}
}

func TestRelationContributionOwnership(t *testing.T) {
	for _, mode := range []Mode{AccessShare, Share, RowExclusive, AccessExclusive} {
		for _, ordinary := range []string{"none", "before", "during"} {
			t.Run(deadlockMode(mode)+"/"+ordinary, func(t *testing.T) {
				f := newLockFixture(t)
				owner, peer := f.tx(), f.tx()
				resource := f.relation()
				if ordinary == "before" {
					if r := f.request(owner, resource, mode); r != nil {
						t.Fatal("ordinary setup queued")
					}
				}
				var outer, inner RelationContribution
				scopedRelationGrant(t, f.m, owner, resource, mode, &outer)
				scopedRelationGrant(t, f.m, owner, resource, mode, &inner)
				if !f.m.HasRelationMode(owner, resource, mode) {
					t.Fatal("scoped grant advertised the wrong arbitration mode", deadlockMode(mode))
				}
				if mode == RowExclusive {
					if r := f.request(peer, resource, RowExclusive); r != nil {
						t.Fatal("scoped RowExclusive blocked a compatible peer")
					}
				}
				waiter := f.request(peer, resource, AccessExclusive)
				if waiter == nil {
					t.Fatal("scoped union did not block peer")
				}
				if ordinary == "during" {
					if r := f.request(owner, resource, mode); r != nil {
						t.Fatal("same-mode ordinary reentry queued")
					}
				}
				// Independent contributions do not require LIFO cleanup.
				f.m.ReleaseRelation(&outer)
				f.m.ReleaseRelation(&outer)
				if !f.m.HasRelationMode(owner, resource, mode) {
					t.Fatal("outer cleanup removed nested arbitration mode")
				}
				select {
				case <-waiter.Done():
					t.Fatal("outer cleanup removed inner contribution")
				default:
				}
				f.m.ReleaseRelation(&inner)
				if ordinary != "none" {
					select {
					case <-waiter.Done():
						t.Fatal("cleanup removed ordinary grant")
					default:
					}
					f.m.End(owner)
				}
				if err := f.result(waiter); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRelationContributionCheckpoint(t *testing.T) {
	for _, mode := range []Mode{AccessShare, Share, RowExclusive, AccessExclusive} {
		t.Run(deadlockMode(mode), func(t *testing.T) {
			f := newLockFixture(t)
			owner, peer := f.tx(), f.tx()
			resource := f.relation()
			var scope RelationContribution
			scopedRelationGrant(t, f.m, owner, resource, mode, &scope)
			mark := f.m.Checkpoint(owner)
			if r := f.request(owner, resource, mode); r != nil {
				t.Fatal("ordinary reentry queued")
			}
			waiter := f.request(peer, resource, AccessExclusive)
			f.m.Restore(owner, mark)
			select {
			case <-waiter.Done():
				t.Fatal("rollback removed live scope")
			default:
			}
			f.m.ReleaseRelation(&scope)
			if err := f.result(waiter); err != nil {
				t.Fatal("checkpoint promoted scope to ordinary grant", err)
			}
			// A checkpoint cannot resurrect an already released scope.
			f.m.Restore(owner, mark)
			if f.m.HasRelationMode(owner, resource, mode) {
				t.Fatal("restored released scope")
			}
		})
	}
}

func TestRelationContributionQueueGeneration(t *testing.T) {
	for _, tc := range []struct{ mode, blocker Mode }{{Share, RowExclusive}, {RowExclusive, Share}, {AccessExclusive, RowExclusive}} {
		mode := tc.mode
		for _, retainPrior := range []bool{false, true} {
			t.Run(deadlockMode(mode)+"/"+map[bool]string{false: "released_prior", true: "retained_prior"}[retainPrior], func(t *testing.T) {
				f := newLockFixture(t)
				owner, blocker, peer := f.tx(), f.tx(), f.tx()
				resource := f.relation()
				if r := f.request(blocker, resource, tc.blocker); r != nil {
					t.Fatal("blocker setup queued")
				}
				var initial RelationContribution
				scopedRelationGrant(t, f.m, owner, resource, AccessShare, &initial)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var canceled RelationContribution
				request, err := f.m.StartAcquireRelation(ctx, owner, resource, mode, Policy{}, &canceled)
				if err != nil || request == nil {
					t.Fatal("missing real scoped upgrade queue", err)
				}
				if !retainPrior {
					f.m.ReleaseRelation(&initial)
				}
				f.m.mu.Lock()
				_, tracked := owner.resources[resource]
				f.m.mu.Unlock()
				if !tracked {
					t.Fatal("scope release lost pending resource generation")
				}
				cancel()
				if err := f.result(request); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				f.m.ReleaseRelation(&canceled)
				if f.m.HasRelationMode(owner, resource, mode) {
					t.Fatal("canceled upgrade retained its requested mode")
				}
				if retainPrior {
					if !f.m.HasRelationMode(owner, resource, AccessShare) {
						t.Fatal("canceled upgrade removed the prior scoped hold")
					}
					f.m.ReleaseRelation(&initial)
				}
				f.m.End(blocker)
				// A new grant on the same resource must not be touched by old handles.
				var fresh RelationContribution
				scopedRelationGrant(t, f.m, owner, resource, mode, &fresh)
				waiter := f.request(peer, resource, AccessExclusive)
				if waiter == nil {
					t.Fatal("fresh scope did not block")
				}
				f.m.ReleaseRelation(&initial)
				f.m.ReleaseRelation(&canceled)
				select {
				case <-waiter.Done():
					t.Fatal("stale cleanup removed fresh generation")
				default:
				}
				f.m.ReleaseRelation(&fresh)
				if err := f.result(waiter); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRelationContributionModes(t *testing.T) {
	f := newLockFixture(t)
	owner, resource := f.tx(), f.relation()
	for _, mode := range []Mode{0, ShareUpdateExclusive, AccessShare | RowExclusive, Share | RowExclusive, RowExclusive | AccessExclusive} {
		var contribution RelationContribution
		request, err := f.m.StartAcquireRelation(context.Background(), owner, resource, mode, Policy{}, &contribution)
		if request != nil || err == nil || contribution.started || contribution.granted {
			t.Fatal("invalid scoped mode changed admission state", mode, request, err)
		}
	}
	if f.m.HasRelationMode(owner, resource, AccessShare|Share|RowExclusive|AccessExclusive) {
		t.Fatal("invalid scoped mode installed a grant")
	}
	var shares [4]RelationContribution
	scopedRelationGrant(t, f.m, owner, resource, AccessShare, &shares[0])
	scopedRelationGrant(t, f.m, owner, resource, Share, &shares[1])
	scopedRelationGrant(t, f.m, owner, resource, RowExclusive, &shares[2])
	scopedRelationGrant(t, f.m, owner, resource, AccessExclusive, &shares[3])
	f.m.ReleaseRelation(&shares[3])
	if f.m.HasRelationMode(owner, resource, AccessExclusive) || !f.m.HasRelationMode(owner, resource, AccessShare) || !f.m.HasRelationMode(owner, resource, Share) || !f.m.HasRelationMode(owner, resource, RowExclusive) {
		t.Fatal("releasing scoped AccessExclusive corrupted another mode slot")
	}
	writer := f.request(f.tx(), resource, RowExclusive)
	if writer == nil {
		t.Fatal("scoped Share did not block a peer writer")
	}
	f.m.ReleaseRelation(&shares[1])
	if err := f.result(writer); err != nil {
		t.Fatal(err)
	}
	if f.m.HasRelationMode(owner, resource, Share) || !f.m.HasRelationMode(owner, resource, AccessShare) || !f.m.HasRelationMode(owner, resource, RowExclusive) {
		t.Fatal("releasing scoped Share corrupted another mode slot")
	}
	f.m.ReleaseRelation(&shares[2])
	if f.m.HasRelationMode(owner, resource, RowExclusive) || !f.m.HasRelationMode(owner, resource, AccessShare) {
		t.Fatal("releasing scoped RowExclusive corrupted AccessShare ownership")
	}
	f.m.ReleaseRelation(&shares[0])
	if f.m.HasRelationMode(owner, resource, AccessShare) {
		t.Fatal("scoped AccessShare survived its release")
	}
}
