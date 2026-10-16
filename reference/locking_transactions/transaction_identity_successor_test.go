package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// REL_18_0 heap_update inherits the ORIGINAL raw lock-only MultiXact into
// the successor; its separately computed predecessor action is a different bag.
func TestReferenceTransactionIdentityMultixactSuccessor(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		other, updater := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity_successor(id INT PRIMARY KEY,v INT CONSTRAINT tx_identity_successor_nonnegative CHECK(v>=0))", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity_successor VALUES(1,0),(2,0)", 2)
		// A plain CHECK fails before a physical tuple update. Savepoint recovery
		// makes an actual top-level assignment observable without an aborted-block
		// inquiry, and must not erase an assignment if one really occurred.
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		identityUnassigned(t, p, other)
		fixture.LockExec(t, p.Ctx, other, "SAVEPOINT rejected", 0)
		_, err := other.ExecContext(p.Ctx, "UPDATE tx_identity_successor SET v=-1 WHERE id=1")
		wantError := fixture.CheckViolation("tx_identity_successor", "tx_identity_successor_nonnegative", "1, -1")
		wantError.Severity, wantError.SeverityUnlocalized = "ERROR", "ERROR"
		fixture.AssertDiagnostic(t, err, &wantError)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK TO rejected", 0)
		identityUnassigned(t, p, other)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)

		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, updater, "BEGIN", 0)
		ax, bx, cx := identityLow(t, identityID(t, p, a)), identityLow(t, identityID(t, p, other)), identityLow(t, identityID(t, p, updater))
		keyBag := []identityMember{{ax, "keysh"}, {bx, "keysh"}}
		identityNoMulti(t, p, observer, func() { identityLock(t, p, a, "tx_identity_successor", 1, "KEY SHARE") })
		keys := identityNewMulti(t, p, observer, func() { identityLock(t, p, other, "tx_identity_successor", 1, "KEY SHARE") }, keyBag)
		identityNoMulti(t, p, observer, func() {
			identityLock(t, p, a, "tx_identity_successor", 2, "KEY SHARE")
			identityLock(t, p, other, "tx_identity_successor", 2, "KEY SHARE")
		})
		actionBag := []identityMember{{ax, "keysh"}, {bx, "keysh"}, {cx, "nokeyupd"}}
		action := identityNewMulti(t, p, observer, func() { fixture.LockExec(t, p.Ctx, updater, "UPDATE tx_identity_successor SET v=v+1 WHERE id=1", 1) }, actionBag)
		// Same updater backend caches the same predecessor action bag. Successors
		// inherit K directly; no fresh K-prime allocation or global interning occurs.
		identityNoMulti(t, p, observer, func() { fixture.LockExec(t, p.Ctx, updater, "UPDATE tx_identity_successor SET v=v+1 WHERE id=2", 1) })
		identityNoMulti(t, p, observer, func() { fixture.LockExec(t, p.Ctx, updater, "UPDATE tx_identity_successor SET v=v+1 WHERE id=1", 1) })
		fixture.LockExec(t, p.Ctx, updater, "COMMIT", 0)
		identityBag(t, p, observer, keys, keyBag)
		identityBag(t, p, observer, action, actionBag)
		identityBoth(t, p, b, observer, fixture.Select("SELECT id,v FROM tx_identity_successor ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(1)}}))
		// Reuse C's physical connection in a fresh transaction; the successor is
		// neither the predecessor action bag nor a plan-captured old owner identity.
		fixture.LockExec(t, p.Ctx, updater, "BEGIN", 0)
		cx2 := identityLow(t, identityID(t, p, updater))
		expandedBag := []identityMember{{ax, "keysh"}, {bx, "keysh"}, {cx2, "keysh"}}
		expanded := identityNewMulti(t, p, observer, func() { identityLock(t, p, updater, "tx_identity_successor", 1, "KEY SHARE") }, expandedBag)
		fixture.LockExec(t, p.Ctx, updater, "ROLLBACK", 0)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		// Row2 still has inherited raw K, including ended B. A's running keysh
		// selects the live-MultiXact upgrade branch, not singleton self collapse.
		exclusiveBag := []identityMember{{ax, "keysh"}, {ax, "forupd"}}
		exclusive := identityNewMulti(t, p, observer, func() { identityLock(t, p, a, "tx_identity_successor", 2, "UPDATE") }, exclusiveBag)
		identityBag(t, p, observer, keys, keyBag)
		identityBag(t, p, observer, action, actionBag)
		identityBag(t, p, observer, expanded, expandedBag)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		identityBag(t, p, observer, exclusive, exclusiveBag)
	})
}
