package lock

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
)

func (f *lockFixture) tupleFact(observer *Ticket, tuple MultiTuple) TupleMaintenance {
	f.t.Helper()
	facts := []TupleMaintenance{{Tuple: tuple}}
	before := f.m.Inspect(observer)
	assigned := observer.AssignedFullXID()
	if err := f.m.CaptureTupleMaintenance(context.Background(), observer, facts); err != nil {
		f.t.Fatal(err)
	}
	if after := f.m.Inspect(observer); !reflect.DeepEqual(before, after) || observer.AssignedFullXID() != assigned {
		f.t.Fatal("detached maintenance capture changed manager state or observer identity")
	}
	return facts[0]
}

func TestPgsurgeryFreezeTupleRenewal(t *testing.T) {
	for _, withLocker := range []bool{false, true} {
		name := "singleton"
		if withLocker {
			name = "multi"
		}
		t.Run(name, func(t *testing.T) {
			for _, rewind := range []bool{false, true} {
				label := "committed_updater_rejects_second_action"
				if rewind {
					label = "rewound_action_does_not_commit_with_owner"
				}
				t.Run(label, func(t *testing.T) {
					f := newLockFixture(t)
					observer, locker, owner, next := f.tx(), f.tx(), f.tx(), f.tx()
					locker.MirrorFullXID(30)
					owner.MirrorFullXID(40)
					next.MirrorFullXID(60)
					tuple := f.multiTuple(1)
					if withLocker {
						f.multiGrant(locker, tuple, ForKeyShare)
						f.multiEmit(locker, tuple, MultiKeyShare)
					}
					checkpoint := f.m.Checkpoint(owner)
					f.multiGrant(owner, tuple, ForNoKeyUpdate)
					if err := f.m.PhysicalEvents(owner, []MultiEvent{{Tuple: tuple, Successor: 2, Mode: MultiNoKeyAction}}); err != nil {
						t.Fatal(err)
					}
					if rewind {
						f.m.Restore(owner, checkpoint)
					}
					retained := f.tupleFact(observer, tuple)
					if !retained.Present || retained.Multi != withLocker || retained.NewestFullXID != 40 {
						t.Fatal("missing real prior action header", retained)
					}
					var history []MultiMember
					if withLocker {
						var err error
						history, err = f.m.MultiXactMembers(f.domain, nil, retained.Xmax)
						if err != nil || len(history) != 2 || history[1].XID != 40 || history[1].Mode != MultiNoKeyAction {
							t.Fatal("missing retained updater membership", history, err)
						}
					}
					owner.MirrorCompletion(ids.TxCommitted)
					f.m.End(owner)
					ended := retained
					ended.Active = withLocker
					if got := f.tupleFact(observer, tuple); got != ended {
						t.Fatal("owner completion erased raw history", got, ended)
					}
					f.multiGrant(next, tuple, ForNoKeyUpdate)
					counter := f.m.NextMultiXact(f.domain)
					err := f.m.PhysicalEvents(next, []MultiEvent{{Tuple: tuple, Successor: 3, Mode: MultiNoKeyAction}})
					if !rewind {
						if err == nil || f.m.NextMultiXact(f.domain) != counter || f.tupleFact(observer, tuple) != ended {
							t.Fatal("valid committed updater was discarded or failed event partially published", err)
						}
						return
					}
					if err != nil {
						t.Fatal("rewound updater incorrectly became committed provenance", err)
					}
					current := f.tupleFact(observer, tuple)
					if !current.Present || !current.Active || current.Multi != withLocker || current.NewestFullXID != 60 {
						t.Fatal("genuine renewal did not install new owner", current)
					}
					successor := tuple
					successor.Version = 3
					if !withLocker {
						if current.Xmax != 60 || f.tupleFact(observer, successor).Present {
							t.Fatal("aborted updater leaked into empty successor", current)
						}
					} else {
						bag, err := f.m.MultiXactMembers(f.domain, nil, current.Xmax)
						if err != nil || len(bag) != 2 || bag[0].XID != 30 || bag[0].Mode != MultiKeyShare || bag[1].XID != 60 || bag[1].Mode != MultiNoKeyAction {
							t.Fatal("renewal retained revoked updater as effective member", bag, err)
						}
						inherited := f.tupleFact(observer, successor)
						if !inherited.Active || !inherited.Multi || inherited.Xmax != retained.Xmax {
							t.Fatal("surviving locker lost complete original raw bag", inherited, retained)
						}
						next.MirrorCompletion(ids.TxCommitted)
						f.m.End(next)
						later := f.tx()
						later.MirrorFullXID(70)
						f.multiGrant(later, successor, ForShare)
						f.multiEmit(later, successor, MultiShare)
						bag, err = f.m.MultiXactMembers(f.domain, nil, f.tupleFact(observer, successor).Xmax)
						if err != nil || len(bag) != 2 || bag[0].XID != 30 || bag[1].XID != 70 || bag[1].Mode != MultiShare {
							t.Fatal("copied aborted action revived through top-level commit", bag, err)
						}
						old, err := f.m.MultiXactMembers(f.domain, nil, retained.Xmax)
						if err != nil || !reflect.DeepEqual(old, history) {
							t.Fatal("renewal mutated immutable historical bag", old, history, err)
						}
					}
					if retained.NewestFullXID != 40 || retained.Multi != withLocker {
						t.Fatal("renewal changed detached prior capture", retained)
					}
				})
			}
		})
	}
}

func TestPgsurgeryFreezeTupleEndedLocker(t *testing.T) {
	for _, endBeforeAction := range []bool{false, true} {
		name := "after_action_before_successor"
		if endBeforeAction {
			name = "before_action"
		}
		t.Run(name, func(t *testing.T) {
			f := newLockFixture(t)
			observer, locker, updater := f.tx(), f.tx(), f.tx()
			locker.MirrorFullXID(30)
			updater.MirrorFullXID(40)
			tuple := f.multiTuple(1)
			f.multiGrant(locker, tuple, ForKeyShare)
			f.multiEmit(locker, tuple, MultiKeyShare)
			captured := f.tupleFact(observer, tuple)
			if endBeforeAction {
				f.m.End(locker)
			}
			f.multiGrant(updater, tuple, ForNoKeyUpdate)
			if !endBeforeAction {
				f.multiEmit(updater, tuple, MultiNoKeyAction)
				f.m.End(locker)
			}
			if err := f.m.PhysicalEvents(updater, []MultiEvent{{Tuple: tuple, Successor: 2, Mode: MultiNoKeyAction}}); err != nil {
				t.Fatal(err)
			}
			successor := tuple
			successor.Version = 2
			if got := f.tupleFact(observer, successor); got.Present || got.Active || got.Xmax != 0 {
				t.Fatal("no original locker survives but successor inherited raw xmax", got)
			}
			if !captured.Present || !captured.Active || captured.Xmax != 30 {
				t.Fatal("real renewal changed old detached capture", captured)
			}
		})
	}
}

func TestPgsurgeryFreezeTupleMaintenanceProvenance(t *testing.T) {
	f := newLockFixture(t)
	observer, a, b := f.tx(), f.tx(), f.tx()
	a.MirrorFullXID(1<<32 | 40)
	b.MirrorFullXID(2<<32 | 50)
	tuple := f.multiTuple(1)
	f.multiGrant(a, tuple, ForKeyShare)
	f.multiEmit(a, tuple, MultiKeyShare)
	singleton := f.tupleFact(observer, tuple)
	if !singleton.Active || singleton.Xmax != 40 || singleton.NewestFullXID != 1<<32|40 {
		t.Fatal("singleton lost genuine full-XID provenance", singleton)
	}
	f.multiGrant(b, tuple, ForShare)
	f.multiEmit(b, tuple, MultiShare)
	multi := f.tupleFact(observer, tuple)
	if !multi.Active || !multi.Multi || multi.NewestFullXID != 2<<32|50 || uint64(multi.Xmax) == multi.NewestFullXID {
		t.Fatal("raw MultiXact identity substituted for actual full-XID provenance", multi)
	}
	a.MirrorCompletion(ids.TxAborted)
	f.m.End(a)
	b.MirrorCompletion(ids.TxCommitted)
	f.m.End(b)
	ended := multi
	ended.Active = false
	if got := f.tupleFact(observer, tuple); got != ended || !multi.Active {
		t.Fatal("ended raw facts lost provenance or changed old capture", got, ended)
	}
	// An unknown member must conservatively dominate a known member, without
	// turning a raw public XID into fabricated full transaction provenance.
	a.MirrorFullXID(0)
	unknown := f.tupleFact(observer, tuple)
	if !unknown.Present || unknown.Active || unknown.NewestFullXID != 0 || unknown.Xmax != multi.Xmax {
		t.Fatal("known member hid unknown raw-member provenance", unknown)
	}
}
