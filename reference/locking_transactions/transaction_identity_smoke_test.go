package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIdentityFeatureSmoke(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity_smoke(id INT PRIMARY KEY,v INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity_smoke VALUES(1,0)", 1)
		fixture.LockExec(t, p.Ctx, a, "BEGIN READ ONLY", 0)
		identityUnassigned(t, p, a)
		id := identityID(t, p, a)
		if identityID(t, p, a) != id {
			t.Fatal("unstable current identity")
		}
		identityStatus(t, p, observer, id, "in progress")
		identityBoth(t, p, b, a, fixture.Select("SELECT age($1::xid) AS current_age,age('1'::xid) AS special_age", []string{"current_age", "special_age"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(2147483647)}}, identityLow(t, id)))
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		identityStatus(t, p, observer, id, "committed")
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		id = identityID(t, p, a)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		identityStatus(t, p, observer, id, "aborted")
		other, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		ax, bx := identityLow(t, identityID(t, p, a)), identityLow(t, identityID(t, p, other))
		identityLock(t, p, a, "tx_identity_smoke", 1, "KEY SHARE")
		want := []identityMember{{ax, "keysh"}, {bx, "sh"}}
		multi := identityNewMulti(t, p, observer, func() { identityLock(t, p, other, "tx_identity_smoke", 1, "SHARE") }, want)
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		identityBag(t, p, observer, multi, want)
		identityBoth(t, p, b, observer, fixture.Select("SELECT * FROM pg_get_multixact_members(NULL::xid)", []string{"xid", "mode"}, []string{"XID", "TEXT"}, nil))
		identityMemberMetadata(t, p, b, observer, multi, want)
	})
}
