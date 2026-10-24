package lock

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func roleLockFixtures(t *testing.T) (*lockFixture, *lockFixture, Resource) {
	t.Helper()
	a := newLockFixture(t)
	b := &lockFixture{t: t, domain: new(ids.Domain), m: a.m, epoch: 7, next: 100}
	if err := a.m.AdmitDomain(b.domain); err != nil {
		t.Fatal(err)
	}
	shared := new(ids.Domain)
	if err := a.m.SetRoleDomain(shared, 3); err != nil {
		t.Fatal(err)
	}
	role := Resource{ObjectRef: shared.Ref(3, ids.ObjectAddress{ClassID: ids.RoleClassID, ObjectID: 17000})}
	return a, b, role
}

func TestRoleTransactionalLockDomains(t *testing.T) {
	a, b, role := roleLockFixtures(t)
	x, y := a.tx(), b.tx()
	foreign := new(ids.Domain)
	invalid := foreign.Tx(foreign.Session(999), 1, 1, &lockFixtureOwner{}, 1)
	if _, err := a.m.Register(invalid); err == nil {
		t.Fatal("unadmitted owner accepted")
	}
	if err := a.m.AdmitDomain(role.Domain()); err == nil {
		t.Fatal("role domain admitted as database")
	}
	if err := a.m.SetRoleDomain(new(ids.Domain), 3); err == nil {
		t.Fatal("shared authority replaced")
	}
	copyTicket := *x
	if _, err := a.m.StartAcquire(context.Background(), &copyTicket, role, AccessShare, Policy{}); err == nil {
		t.Fatal("copied owner accepted")
	}
	if _, err := a.m.StartAcquire(context.Background(), y, CompletionOf(&copyTicket), TransactionCompletion, Policy{}); err == nil {
		t.Fatal("copied completion owner accepted")
	}
	local := a.relation()
	if _, err := a.m.StartAcquire(context.Background(), y, local, AccessShare, Policy{}); err == nil {
		t.Fatal("peer database resource accepted")
	}
	a.request(x, local, AccessExclusive)
	peerLocal := Resource{ObjectRef: b.domain.Ref(b.epoch, local.Address)}
	if b.request(y, peerLocal, AccessExclusive) != nil {
		t.Fatal("equal local OIDs conflicted across databases")
	}
	localName := transactionalNameResource(a, "same_local_name")
	peerName := localName
	peerName.ObjectRef = b.domain.Ref(b.epoch, localName.Address)
	a.request(x, localName, AccessExclusive)
	if b.request(y, peerName, AccessExclusive) != nil {
		t.Fatal("equal local names conflicted across databases")
	}
	forged := role
	forged.ObjectRef = a.domain.Ref(role.Epoch, role.Address)
	if _, err := a.m.StartAcquire(context.Background(), x, forged, AccessExclusive, Policy{}); err == nil {
		t.Fatal("forged role authority accepted")
	}
	a.request(x, role, ShareUpdateExclusive)
	if b.request(y, role, AccessShare) != nil {
		t.Fatal("role SUE blocked dependency AS")
	}
	tuple := role
	tuple.RoleTuple = true
	if b.request(y, tuple, AccessExclusive) != nil {
		t.Fatal("tuple claim aliased role object")
	}
	name := Resource{ObjectRef: role.Domain().Ref(role.Epoch, ids.ObjectAddress{}), Name: ids.NameKey{ClassID: ids.RoleClassID, Name: "exact_role"}}
	if a.request(x, name, AccessExclusive) != nil {
		t.Fatal("name claim aliased role identity")
	}
	x.MirrorFullXID(31)
	y.MirrorFullXID(47)
	wait := b.request(y, name, AccessExclusive)
	if wait == nil {
		t.Fatal("shared name did not queue")
	}
	rows, err := b.m.PGLocks(b.domain, b.epoch)
	if err != nil {
		t.Fatal(err)
	}
	foundWait, foundRole := false, false
	for _, row := range rows {
		if row.Waiting && row.Owner.Session == y.Session().ID() && row.Kind == TransactionRecord && row.XID == 31 {
			foundWait = true
		}
		if row.Kind == ObjectRecord && row.Class == uint32(ids.RoleClassID) && row.Shared && row.Object == 17000 {
			foundRole = true
		}
		if row.Waiting && row.Kind == ObjectRecord {
			t.Fatal("internal claim invented object wait", row)
		}
	}
	if !foundWait || !foundRole {
		t.Fatal("missing real XID/shared role projection", rows)
	}
	pids, err := a.m.BlockingPids(a.domain, y.Session().ID())
	if err != nil || len(pids) != 1 || pids[0] != int32(x.Session().ID()) {
		t.Fatal("cross database shared blocker", pids, err)
	}
	// Cross-database completion uses exact tickets, not numeric epoch equality.
	completion := a.request(x, CompletionOf(y), TransactionCompletion)
	var deadlock *sqlstate.Error
	if err := a.result(completion); !errors.As(err, &deadlock) || deadlock.Code != "40P01" {
		t.Fatal("shared name/completion graph missed cycle", err)
	}
	a.m.End(x)
	if err := b.result(wait); err != nil {
		t.Fatal(err)
	}
}

func TestRoleTransactionalLockLifecycle(t *testing.T) {
	t.Run("savepoint_cancel_end", func(t *testing.T) {
		a, b, role := roleLockFixtures(t)
		x, y := a.tx(), b.tx()
		a.request(x, role, AccessShare)
		mark := a.m.Checkpoint(x)
		a.request(x, role, AccessExclusive)
		tuple := role
		tuple.RoleTuple = true
		a.request(x, tuple, AccessExclusive)
		waiter := b.request(y, role, ShareUpdateExclusive)
		if waiter == nil {
			t.Fatal("role upgrade failed to block")
		}
		a.m.Restore(x, mark)
		if err := b.result(waiter); err != nil {
			t.Fatal(err)
		}
		if !a.m.Holds(x, role, AccessShare) || a.m.Holds(x, role, AccessExclusive) || a.m.Holds(x, tuple, AccessExclusive) {
			t.Fatal("savepoint contributions not restored")
		}
		ctx, cancel := context.WithCancel(context.Background())
		canceled, err := a.m.StartAcquire(ctx, x, role, AccessExclusive, Policy{Timeout: time.Hour})
		if err != nil || canceled == nil {
			t.Fatal(canceled, err)
		}
		cancel()
		if err := a.result(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		a.m.ExpireLockTimeout(canceled)
		if err := a.result(canceled); !errors.Is(err, context.Canceled) || !canceled.Released() {
			t.Fatal("terminal cancellation replaced or callback retained", err)
		}
		timed, err := a.m.StartAcquire(context.Background(), x, role, AccessExclusive, Policy{Timeout: time.Hour})
		if err != nil || timed == nil {
			t.Fatal(timed, err)
		}
		a.m.ExpireWait(timed, timed.Deadline().Add(time.Nanosecond))
		var timeout *sqlstate.Error
		if err := a.result(timed); !errors.As(err, &timeout) || timeout.Code != "55P03" || !timed.Released() {
			t.Fatal("lock timeout not terminal/released", err)
		}
		completion := a.request(x, CompletionOf(y), TransactionCompletion)
		a.m.DetachPrepared(y)
		a.m.End(y)
		if err := a.result(completion); err != nil {
			t.Fatal("prepared completion not released", err)
		}
		a.m.End(x)
		a.m.Restore(x, mark)
		a.empty()
	})
	t.Run("tuple_wait_actual_xid", func(t *testing.T) {
		a, b, tuple := roleLockFixtures(t)
		tuple.RoleTuple = true
		x, y := a.tx(), b.tx()
		x.MirrorFullXID(67)
		a.request(x, tuple, AccessExclusive)
		waiter := b.request(y, tuple, AccessExclusive)
		if waiter == nil {
			t.Fatal("tuple writers failed to serialize")
		}
		rows, err := a.m.PGLocks(a.domain, a.epoch)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range rows {
			if row.Kind == ObjectRecord {
				t.Fatal("tuple claim fabricated physical object lock", row)
			}
			if row.Waiting && row.Kind == TransactionRecord && row.XID == 67 && row.Owner.Session == y.Session().ID() {
				found = true
			}
		}
		if !found {
			t.Fatal("tuple claim omitted actual blocker XID", rows)
		}
		a.m.End(x)
		if err := b.result(waiter); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("database_fence_and_physical_isolation", func(t *testing.T) {
		a, b, role := roleLockFixtures(t)
		x, y, z := a.tx(), b.tx(), b.tx()
		a.request(x, role, AccessShare)
		b.request(y, role, AccessShare)
		waiting := b.request(z, role, AccessExclusive)
		if waiting == nil {
			t.Fatal("AX bypassed shared holds")
		}
		// Identical advisory numbers are local, including persistent lifetime.
		pa, pb := a.advisoryParticipant(), b.advisoryParticipant()
		ka := Resource{Database: a.domain, Kind: AdvisoryBigintResource, Low: 12}
		kb := ka
		kb.Database = b.domain
		a.advisoryGrant(pa, ka, AdvisoryExclusive)
		b.advisoryGrant(pb, kb, AdvisoryExclusive)
		if _, err := a.m.StartAcquire(context.Background(), x, kb, AdvisoryExclusive, Policy{}); err == nil {
			t.Fatal("foreign advisory scope accepted")
		}
		// Each database may allocate MultiXact 1 from its real local members.
		for _, f := range []*lockFixture{a, b} {
			p, q := f.tx(), f.tx()
			p.MirrorFullXID(3)
			q.MirrorFullXID(4)
			tuple := f.multiTuple(1)
			f.multiGrant(p, tuple, ForKeyShare)
			f.multiEmit(p, tuple, MultiKeyShare)
			f.multiGrant(q, tuple, ForShare)
			f.multiEmit(q, tuple, MultiShare)
			f.multiWant(1, "3/keysh", "4/sh")
		}
		a.m.Fence(a.domain, a.epoch+1)
		snapshot := a.m.Inspect(x, y)
		if snapshot.Valid(x) || !snapshot.Valid(y) || !a.m.Holds(y, role, AccessShare) {
			t.Fatal("database fence retired peer role owner")
		}
		if snapshot.Resources[role].Holders[y].Mode != AccessShare || len(snapshot.Resources[role].Pending) != 1 {
			t.Fatal("fence lost peer shared queue")
		}
		if snapshot.MultiHistory[a.domain] != 0 || snapshot.MultiHistory[b.domain] != 1 || snapshot.MultiNext[b.domain] != 2 {
			t.Fatal("fence lost peer physical history", snapshot.MultiHistory)
		}
		b.multiWant(1, "3/keysh", "4/sh")
		if a.advisoryHold(pa, ka).Mode != AdvisoryExclusive || b.advisoryHold(pb, kb).Mode != AdvisoryExclusive {
			t.Fatal("database fence lost persistent advisory grant")
		}
		select {
		case <-waiting.Done():
			t.Fatal("fence granted through peer hold")
		default:
		}
		b.m.End(y)
		if err := b.result(waiting); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRoleTransactionalLockDeadlock(t *testing.T) {
	for _, crossDatabase := range []bool{false, true} {
		name := "mixed_relation_role"
		if crossDatabase {
			name = "cross_database_roles"
		}
		t.Run(name, func(t *testing.T) {
			a, b, role := roleLockFixtures(t)
			x := a.tx()
			var y *Ticket
			first := a.relation()
			firstMode, secondMode := AccessExclusive, AccessShare
			if crossDatabase {
				y = b.tx()
				first = role
				first.Address.ObjectID++
				firstMode, secondMode = ShareUpdateExclusive, ShareUpdateExclusive
			} else {
				y = a.tx()
			}
			a.request(x, first, firstMode)
			a.request(y, role, ShareUpdateExclusive)
			survivor := a.request(x, role, ShareUpdateExclusive)
			victim := a.request(y, first, secondMode)
			var diagnostic *sqlstate.Error
			if err := a.result(victim); !errors.As(err, &diagnostic) || diagnostic.Code != "40P01" {
				t.Fatal("canonical mixed graph missed cycle", err)
			}
			if !strings.Contains(diagnostic.Detail, " on role ") || len(strings.Split(diagnostic.Detail, "\n")) != 2 {
				t.Fatal("incomplete role cycle evidence", diagnostic.Detail)
			}
			if !crossDatabase && !strings.Contains(diagnostic.Detail, " on relation ") {
				t.Fatal("mixed relation edge missing", diagnostic.Detail)
			}
			a.m.End(y)
			if err := a.result(survivor); err != nil {
				t.Fatal(err)
			}
			a.m.End(x)
			a.empty()
		})
	}
}
