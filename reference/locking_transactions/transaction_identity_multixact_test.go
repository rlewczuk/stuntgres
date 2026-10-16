package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIdentityMultixact(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		other, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity_members(id INT PRIMARY KEY,v INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity_members VALUES(1,0),(2,0),(3,0),(4,0),(5,0),(6,0)", 6)
		fixture.LockExec(t, p.Ctx, observer, "BEGIN READ ONLY", 0)
		identityUnassigned(t, p, observer)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		ax, bx := identityLow(t, identityID(t, p, a)), identityLow(t, identityID(t, p, other))
		identityStatus(t, p, observer, identityID(t, p, a), "in progress")
		identityUnassigned(t, p, observer)
		history := make(map[uint32][]identityMember)
		for _, tc := range []struct {
			row          int
			mode, member string
		}{{1, "SHARE", "sh"}, {2, "NO KEY UPDATE", "fornokeyupd"}, {3, "", "nokeyupd"}, {4, "KEY SHARE", "keysh"}} {
			identityNoMulti(t, p, observer, func() { identityLock(t, p, a, "tx_identity_members", tc.row, "KEY SHARE") })
			want := []identityMember{{ax, "keysh"}, {bx, tc.member}}
			id := identityNewMulti(t, p, observer, func() {
				if tc.mode == "" {
					fixture.LockExec(t, p.Ctx, other, "UPDATE tx_identity_members SET v=v+1 WHERE id=3", 1)
				} else {
					identityLock(t, p, other, "tx_identity_members", tc.row, tc.mode)
				}
			}, want)
			history[id] = want
			if tc.mode != "" {
				identityNoMulti(t, p, observer, func() { identityLock(t, p, other, "tx_identity_members", tc.row, tc.mode) })
			}
		}
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		for id, want := range history {
			identityBag(t, p, observer, id, want)
		}
		want := []identityMember{{ax, "keysh"}, {ax, "forupd"}}
		id := identityNewMulti(t, p, observer, func() { identityLock(t, p, a, "tx_identity_members", 4, "UPDATE") }, want)
		history[id] = want
		want = []identityMember{{ax, "keysh"}, {ax, "forupd"}, {ax, "upd"}}
		id = identityNewMulti(t, p, observer, func() { fixture.LockExec(t, p.Ctx, a, "DELETE FROM tx_identity_members WHERE id=4", 1) }, want)
		history[id] = want
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		for id, want := range history {
			identityBag(t, p, observer, id, want)
		}
		identityUnassigned(t, p, observer)
		fixture.LockExec(t, p.Ctx, observer, "COMMIT", 0)
	})
}

func TestReferenceTransactionIdentityMultixactLifecycle(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		other, third := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity_events(id INT PRIMARY KEY,v INT CONSTRAINT tx_identity_nonnegative CHECK(v>=0))", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity_events VALUES(1,0),(2,0),(3,0),(4,0),(5,0),(6,0)", 6)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		ax := identityLow(t, identityID(t, p, a))
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		bx := identityLow(t, identityID(t, p, other))
		identityLock(t, p, a, "tx_identity_events", 1, "KEY SHARE")
		identityNoMulti(t, p, observer, func() {
			_, err := other.ExecContext(p.Ctx, "UPDATE tx_identity_events SET v=-1 WHERE id=1")
			want := fixture.CheckViolation("tx_identity_events", "tx_identity_nonnegative", "1, -1")
			want.Severity = "ERROR"
			want.SeverityUnlocalized = "ERROR"
			fixture.AssertDiagnostic(t, err, &want)
		})
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		bx = identityLow(t, identityID(t, p, other))
		identityLock(t, p, a, "tx_identity_events", 2, "KEY SHARE")
		falseBag := []identityMember{{ax, "keysh"}, {bx, "fornokeyupd"}}
		falseID := identityNewMulti(t, p, observer, func() {
			fixture.LockExec(t, p.Ctx, other, "INSERT INTO tx_identity_events VALUES(2,99) ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v WHERE false", 0)
		}, falseBag)
		identityBoth(t, p, b, observer, fixture.Select("SELECT v FROM tx_identity_events WHERE id=2", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}}))
		identityNoMulti(t, p, observer, func() {
			fixture.LockExec(t, p.Ctx, other, "INSERT INTO tx_identity_events VALUES(2,99) ON CONFLICT(id) DO UPDATE SET v=EXCLUDED.v WHERE false", 0)
		})
		fixture.LockExec(t, p.Ctx, other, "SAVEPOINT extra_lock", 0)
		identityNoMulti(t, p, observer, func() { identityLock(t, p, other, "tx_identity_events", 5, "UPDATE") })
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK TO extra_lock", 0)
		fixture.LockExec(t, p.Ctx, other, "RELEASE extra_lock", 0)
		identityBag(t, p, observer, falseID, falseBag)
		fixture.LockExec(t, p.Ctx, third, "BEGIN", 0)
		// Savepoint rollback released the later physical grant, not old history.
		identityNoMulti(t, p, observer, func() { identityLock(t, p, third, "tx_identity_events", 5, "UPDATE NOWAIT") })
		identityNoMulti(t, p, observer, func() {
			identityBoth(t, p, b, third, fixture.Select("SELECT id FROM tx_identity_events WHERE id=99 FOR UPDATE", []string{"id"}, []string{"INT4"}, nil))
		})
		identityNoMulti(t, p, observer, func() {
			rows, err := third.QueryContext(p.Ctx, "SELECT id FROM tx_identity_events WHERE id=2 FOR UPDATE SKIP LOCKED")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("SKIP LOCKED: %v", rows.Err())
			}
		})
		identityNoMulti(t, p, observer, func() {
			_, err := third.ExecContext(p.Ctx, "SELECT id FROM tx_identity_events WHERE id=2 FOR UPDATE NOWAIT")
			want := fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on row in relation "tx_identity_events"`)
			fixture.AssertDiagnostic(t, err, &want)
		})
		fixture.LockExec(t, p.Ctx, third, "ROLLBACK", 0)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		identityBag(t, p, observer, falseID, falseBag)
		// Exact-pair reentry does not prune ended B from the old record.
		identityNoMulti(t, p, observer, func() { identityLock(t, p, a, "tx_identity_events", 2, "KEY SHARE") })
		identityBag(t, p, observer, falseID, falseBag)
		fixture.LockExec(t, p.Ctx, third, "BEGIN", 0)
		cx := identityLow(t, identityID(t, p, third))
		expanded := []identityMember{{ax, "keysh"}, {cx, "sh"}}
		expandedID := identityNewMulti(t, p, observer, func() { identityLock(t, p, third, "tx_identity_events", 2, "SHARE") }, expanded)
		identityBag(t, p, observer, falseID, falseBag)
		fixture.LockExec(t, p.Ctx, third, "ROLLBACK", 0)
		identityBag(t, p, observer, expandedID, expanded)
		// A genuine action can precede a later RETURNING error. It remains in history.
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		bx = identityLow(t, identityID(t, p, other))
		identityLock(t, p, a, "tx_identity_events", 3, "KEY SHARE")
		actionBag := []identityMember{{ax, "keysh"}, {bx, "nokeyupd"}}
		actionID := identityNewMulti(t, p, observer, func() {
			_, err := other.ExecContext(p.Ctx, "UPDATE tx_identity_events SET v=v+1 WHERE id=3 RETURNING 1/(v-v)")
			want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
			fixture.AssertDiagnostic(t, err, &want)
		}, actionBag)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		identityBag(t, p, observer, actionID, actionBag)
		// Expansion drops an aborted updater, but preserves the old action bag.
		fixture.LockExec(t, p.Ctx, third, "BEGIN", 0)
		cx = identityLow(t, identityID(t, p, third))
		filtered := []identityMember{{ax, "keysh"}, {cx, "sh"}}
		identityNewMulti(t, p, observer, func() { identityLock(t, p, third, "tx_identity_events", 3, "SHARE") }, filtered)
		identityBag(t, p, observer, actionID, actionBag)
		fixture.LockExec(t, p.Ctx, third, "ROLLBACK", 0)
		// Committed updater remains on a compatible old tuple for subsequent lockers.
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		bx = identityLow(t, identityID(t, p, other))
		identityLock(t, p, a, "tx_identity_events", 4, "KEY SHARE")
		committedBag := []identityMember{{ax, "keysh"}, {bx, "nokeyupd"}}
		committedID := identityNewMulti(t, p, observer, func() { fixture.LockExec(t, p.Ctx, other, "UPDATE tx_identity_events SET v=v+1 WHERE id=4", 1) }, committedBag)
		fixture.LockExec(t, p.Ctx, other, "COMMIT", 0)
		identityBag(t, p, observer, committedID, committedBag)
		// PREPARE keeps the actual owner and immutable record; completion only ends it.
		fixture.PreparedReferencePrepare(t, p, b, a, "tx_identity_members_prepared")
		identityBag(t, p, observer, committedID, committedBag)
		fixture.LockExec(t, p.Ctx, observer, "ROLLBACK PREPARED 'tx_identity_members_prepared'", 0)
		identityBag(t, p, observer, committedID, committedBag)
		identityMemberMetadata(t, p, b, observer, committedID, committedBag)
		identityQueue(t, p, b, a, other, third, observer)
		identityRecheck(t, p, b, a, other, third, observer)
		identityMemberControls(t, p, b, a, other, observer)
	})
}
