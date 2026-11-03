package lock

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

// The fixture's ordinary diagnostic helper requires empty Detail/Hint and is
// deliberately inappropriate for a complete hard-cycle diagnostic.
func statDatabaseDeadlockDiagnostic(t *testing.T, err error, a, b *Ticket, x, y Resource) {
	t.Helper()
	edge := func(waiter, blocker *Ticket, resource Resource) string {
		return fmt.Sprintf("Transaction %d (session %d, epoch %d) waits for AccessExclusiveLock on relation %d (class %d, sub-ID %d, epoch %d); blocked by transaction %d (session %d, epoch %d).",
			waiter.Ref().ID(), waiter.Ref().Session().ID(), waiter.Ref().Epoch(), resource.Address.ObjectID, resource.Address.ClassID, resource.Address.SubID, resource.Epoch,
			blocker.Ref().ID(), blocker.Ref().Session().ID(), blocker.Ref().Epoch())
	}
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected",
		Detail: edge(a, b, y) + "\n" + edge(b, a, x), Hint: "See server log for query details."}
	var got *sqlstate.Error
	reversed := want
	reversed.Detail = edge(b, a, x) + "\n" + edge(a, b, y)
	// Either participant may anchor cycle detection. Require both exact,
	// directed edges and every diagnostic field, but not a chosen rotation.
	if !errors.As(err, &got) || *got != want && *got != reversed {
		t.Fatalf("complete hard-cycle diagnostic = %#v (%v), want %#v or %#v", got, err, want, reversed)
	}
}

func TestStatDatabaseDeadlockDecision(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "hard_victim"
		}
		t.Run(name, func(t *testing.T) {
			f := newLockFixture(t)
			a, b := f.tx(), f.tx()
			a.SetStatisticsCounts(enabled)
			b.SetStatisticsCounts(enabled)
			x, y := f.relation(), f.relation()
			f.request(a, x, AccessExclusive)
			f.request(b, y, AccessExclusive)
			one := f.request(a, y, AccessExclusive)
			two := f.request(b, x, AccessExclusive)
			victim, survivor, owner := two, one, b
			if one.Terminal() {
				victim, survivor, owner = one, two, a
			}
			statDatabaseDeadlockDiagnostic(t, f.result(victim), a, b, x, y)
			want := int64(0)
			if enabled {
				want = 1
			}
			if got := owner.TakeDeadlocks(); got != want {
				t.Fatal("selected real victim", got, want)
			}
			if a.TakeDeadlocks() != 0 || b.TakeDeadlocks() != 0 {
				t.Fatal("double counted victim")
			}
			f.m.End(owner)
			if err := f.result(survivor); err != nil {
				t.Fatal(err)
			}
			f.m.End(a)
			f.m.End(b)
		})
	}
	t.Run("request_policy_captured", func(t *testing.T) {
		f := newLockFixture(t)
		a, b := f.tx(), f.tx()
		a.SetStatisticsCounts(true)
		b.SetStatisticsCounts(true)
		x, y := f.relation(), f.relation()
		f.request(a, x, AccessExclusive)
		f.request(b, y, AccessExclusive)
		one := f.request(a, y, AccessExclusive)
		a.SetStatisticsCounts(false)
		two := f.request(b, x, AccessExclusive)
		owner, victim, survivor := b, two, one
		if one.Terminal() {
			owner, victim, survivor = a, one, two
		}
		statDatabaseDeadlockDiagnostic(t, f.result(victim), a, b, x, y)
		if owner.TakeDeadlocks() != 1 {
			t.Fatal("later policy erased eligible wait")
		}
		f.m.End(owner)
		if err := f.result(survivor); err != nil {
			t.Fatal(err)
		}
	})
	for _, cancelled := range []bool{false, true} {
		name := "timeout_wins"
		if cancelled {
			name = "cancel_wins"
		}
		t.Run(name, func(t *testing.T) {
			f := newLockFixture(t)
			a, b := f.tx(), f.tx()
			a.SetStatisticsCounts(true)
			b.SetStatisticsCounts(true)
			x, y := f.relation(), f.relation()
			f.request(a, x, AccessExclusive)
			f.request(b, y, AccessExclusive)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			one, err := f.m.StartAcquire(ctx, a, y, AccessExclusive, Policy{Timeout: time.Hour})
			if err != nil || one == nil {
				t.Fatal(one, err)
			}
			if cancelled {
				cancel()
			} else {
				f.m.mu.Lock()
				one.timer.Stop()
				one.deadline = time.Now().Add(-time.Second)
				f.m.mu.Unlock()
			}
			two := f.request(b, x, AccessExclusive)
			_ = f.result(one)
			if two.Terminal() || a.TakeDeadlocks() != 0 || b.TakeDeadlocks() != 0 {
				t.Fatal("interruption manufactured hard victim")
			}
			f.m.End(a)
			if err := f.result(two); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("soft_reorder", func(t *testing.T) {
		f := newLockFixture(t)
		a, b, c := f.tx(), f.tx(), f.tx()
		for _, ticket := range []*Ticket{a, b, c} {
			ticket.SetStatisticsCounts(true)
		}
		x, y := f.relation(), f.relation()
		f.request(a, x, AccessShare)
		f.request(c, y, AccessExclusive)
		rb := f.request(b, x, AccessExclusive)
		rc := f.request(c, x, AccessShare)
		ra := f.request(a, y, AccessShare)
		if err := f.result(rc); err != nil {
			t.Fatal("compatible follower not reordered", err)
		}
		if a.TakeDeadlocks() != 0 || b.TakeDeadlocks() != 0 || c.TakeDeadlocks() != 0 {
			t.Fatal("soft reorder counted")
		}
		f.m.End(c)
		if err := f.result(ra); err != nil {
			t.Fatal(err)
		}
		f.m.End(a)
		if err := f.result(rb); err != nil {
			t.Fatal(err)
		}
	})
}
