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
	for _, mode := range []Mode{AccessShare, Share} {
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
	for _, mode := range []Mode{AccessShare, Share} {
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
	f := newLockFixture(t)
	owner, blocker, peer := f.tx(), f.tx(), f.tx()
	resource := f.relation()
	if r := f.request(blocker, resource, RowExclusive); r != nil {
		t.Fatal("blocker setup queued")
	}
	var initial RelationContribution
	scopedRelationGrant(t, f.m, owner, resource, AccessShare, &initial)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var canceled RelationContribution
	request, err := f.m.StartAcquireRelation(ctx, owner, resource, Share, Policy{}, &canceled)
	if err != nil || request == nil {
		t.Fatal("missing real scoped upgrade queue", err)
	}
	f.m.ReleaseRelation(&initial)
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
	f.m.End(blocker)
	// A new grant on the same resource must not be touched by old handles.
	var fresh RelationContribution
	scopedRelationGrant(t, f.m, owner, resource, Share, &fresh)
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
}
