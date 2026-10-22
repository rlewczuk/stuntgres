package engine

import (
	"context"
	"stuntgres/internal/engine/lock"
	"testing"
)

func TestPgrowlocksEligibility(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, mode := range []lock.MultiMode{lock.MultiKeyShare, lock.MultiShare, lock.MultiNoKeyLock, lock.MultiUpdateLock, lock.MultiNoKeyAction, lock.MultiUpdateAction} {
			e := New()
			_, a := blockingUnitOwner(t, e)
			_, observer := blockingUnitOwner(t, e)
			pgLocksUnitAssign(t, e, a)
			tuple := identityMultiUnitTuple(e, 1)
			grant := lock.ForUpdate
			if mode == lock.MultiKeyShare {
				grant = lock.ForKeyShare
			}
			if mode == lock.MultiShare {
				grant = lock.ForShare
			}
			if mode == lock.MultiNoKeyLock || mode == lock.MultiNoKeyAction {
				grant = lock.ForNoKeyUpdate
			}
			identityMultiGrant(t, e, a, tuple, grant)
			identityMultiEmit(t, e, a, tuple, mode)
			expected := 1
			if private && mode >= lock.MultiNoKeyAction {
				expected = 0
			}
			if rows := pgrowlocksUnitCapture(t, e, observer, tuple, private); len(rows) != expected {
				t.Fatal("singleton HTSU insertion/header branch", private, mode, rows)
			}
			own := 1
			if mode >= lock.MultiNoKeyAction {
				own = 0
			}
			if rows := pgrowlocksUnitCapture(t, e, a, tuple, private); len(rows) != own {
				t.Fatal("caller HTSU", private, mode, rows)
			}
		}
	}
	t.Run("promotion_and_inherited_singleton", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, observer := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		tuple := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, tuple, lock.ForUpdate)
		identityMultiEmit(t, e, a, tuple, lock.MultiUpdateLock)
		if err := e.locks.PhysicalEvents(a, []lock.MultiEvent{{Tuple: tuple, Mode: lock.MultiNoKeyAction, Successor: 2}}); err != nil {
			t.Fatal(err)
		}
		rows := pgrowlocksUnitCapture(t, e, observer, tuple, false)
		if len(rows) != 1 || rows[0].Multi || len(rows[0].Members) != 1 || rows[0].Members[0].Mode != lock.MultiUpdateAction {
			t.Fatal("prior FOR UPDATE must promote non-key action to Update", rows)
		}
		if own := pgrowlocksUnitCapture(t, e, a, tuple, false); len(own) != 0 {
			t.Fatal("own modified predecessor", own)
		}
		successor := tuple
		successor.Version = 2
		inherited := pgrowlocksUnitCapture(t, e, a, successor, true)
		if len(inherited) != 1 || inherited[0].Multi || inherited[0].Members[0].Mode != lock.MultiKeyShare {
			t.Fatal("inherited singleton physical header", inherited)
		}
		checkpoint := e.locks.Checkpoint(a)
		identityMultiEmit(t, e, a, successor, lock.MultiShare)
		relocked := pgrowlocksUnitCapture(t, e, a, successor, true)
		if len(relocked) != 1 || relocked[0].Members[0].Mode != lock.MultiShare {
			t.Fatal("new grant failed to replace inherited singleton header", relocked)
		}
		e.locks.Restore(a, checkpoint)
		restored := pgrowlocksUnitCapture(t, e, a, successor, true)
		if len(restored) != 1 || restored[0].Members[0].Mode != lock.MultiKeyShare {
			t.Fatal("contribution rollback lost inherited header", restored)
		}
		fresh := identityMultiUnitTuple(e, 3)
		identityMultiGrant(t, e, a, fresh, lock.ForNoKeyUpdate)
		identityMultiEmit(t, e, a, fresh, lock.MultiNoKeyAction)
		unpromoted := pgrowlocksUnitCapture(t, e, observer, fresh, false)
		if len(unpromoted) != 1 || unpromoted[0].Members[0].Mode != lock.MultiNoKeyAction {
			t.Fatal("fresh non-key action was spuriously promoted", unpromoted)
		}
	})
	t.Run("mixed_multi_current_and_ended_updater", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		bs, b := blockingUnitOwner(t, e)
		_, observer := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, b)
		tuple := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, tuple, lock.ForKeyShare)
		identityMultiEmit(t, e, a, tuple, lock.MultiKeyShare)
		identityMultiGrant(t, e, b, tuple, lock.ForNoKeyUpdate)
		identityMultiEmit(t, e, b, tuple, lock.MultiNoKeyAction)
		if rows := pgrowlocksUnitCapture(t, e, b, tuple, false); len(rows) != 0 {
			t.Fatal("own Multi updater", rows)
		}
		if rows := pgrowlocksUnitCapture(t, e, observer, tuple, true); len(rows) != 1 || !rows[0].Multi {
			t.Fatal("private insertion non-lock-only Multi branch", rows)
		}
		transactionEnd(t, bs, b.Ref(), true)
		rows := pgrowlocksUnitCapture(t, e, observer, tuple, false)
		if len(rows) != 1 || !rows[0].Multi || len(rows[0].Members) != 2 || rows[0].Members[1].Mode != lock.MultiNoKeyAction || rows[0].Members[1].PID != 0 {
			t.Fatal("RR predecessor compatible survivor after updater commits", rows)
		}
	})
	t.Run("rolled_back_action_does_not_make_caller_updater", func(t *testing.T) {
		e := New()
		_, a := blockingUnitOwner(t, e)
		_, b := blockingUnitOwner(t, e)
		pgLocksUnitAssign(t, e, a)
		pgLocksUnitAssign(t, e, b)
		tuple := identityMultiUnitTuple(e, 1)
		identityMultiGrant(t, e, a, tuple, lock.ForKeyShare)
		identityMultiEmit(t, e, a, tuple, lock.MultiKeyShare)
		identityMultiGrant(t, e, b, tuple, lock.ForKeyShare)
		identityMultiEmit(t, e, b, tuple, lock.MultiKeyShare)
		saved := e.locks.Checkpoint(b)
		identityMultiGrant(t, e, b, tuple, lock.ForNoKeyUpdate)
		identityMultiEmit(t, e, b, tuple, lock.MultiNoKeyAction)
		e.locks.Restore(b, saved)
		rows := pgrowlocksUnitCapture(t, e, b, tuple, false)
		if len(rows) != 1 || !rows[0].Multi || len(rows[0].Members) != 3 || rows[0].Members[2].Mode != lock.MultiNoKeyAction {
			t.Fatal("immutable raw action versus effective provenance", rows)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if rows, err := e.locks.Pgrowlocks(ctx, b, []lock.PgrowlocksTuple{{Tuple: tuple}}); err == nil || rows != nil {
			t.Fatal("canceled capture", rows, err)
		}
	})
	pgrowlocksVisibilityUnits(t)
}
