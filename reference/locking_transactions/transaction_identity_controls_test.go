package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Logical controls have no PostgreSQL equivalent. The shared SQL already proves
// retention on ordinary transaction ends; this phase tests actual native controls.
func identityMemberControls(t *testing.T, p *fixture.Pair, b fixture.Backend, a, other, observer *sql.Conn) {
	t.Helper()
	if b.PG {
		return
	}
	for _, restore := range []bool{false, true} {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity_retire(id INT PRIMARY KEY)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity_retire VALUES(1)", 1)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, other, "BEGIN", 0)
		aFull, bFull := identityID(t, p, a), identityID(t, p, other)
		ax, bx := identityLow(t, aFull), identityLow(t, bFull)
		identityLock(t, p, a, "tx_identity_retire", 1, "KEY SHARE")
		want := []identityMember{{ax, "keysh"}, {bx, "sh"}}
		id := identityNewMulti(t, p, observer, func() { identityLock(t, p, other, "tx_identity_retire", 1, "SHARE") }, want)
		fixture.LockExec(t, p.Ctx, other, "ROLLBACK", 0)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil {
			t.Fatal("malformed restore succeeded")
		}
		identityBag(t, p, observer, id, want)
		identityStatus(t, p, observer, aFull, "in progress")
		identityStatus(t, p, observer, bFull, "aborted")
		before := identityNextMulti(t, p, observer)
		var err error
		if restore {
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE tx_identity_replacement(v INT);"))
		} else {
			err = stuntgres.Reset(p.Ctx, p.Native)
		}
		if err != nil {
			t.Fatal(err)
		}
		if identityNextMulti(t, p, observer) != before {
			t.Fatal("replacement reset MultiXact counter")
		}
		_, err = a.ExecContext(p.Ctx, "COMMIT")
		fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
		for _, full := range []string{aFull, bFull} {
			identityBoth(t, p, b, observer, fixture.Select("SELECT pg_xact_status($1::xid8) AS s", []string{"s"}, []string{"TEXT"}, [][]any{{nil}}, full))
		}
		n := strconv.FormatUint(uint64(id), 10)
		identityError(t, p, b, observer, "SELECT * FROM pg_get_multixact_members($1::xid)", fixture.Diagnostic{Code: "XX000", Message: "MultiXactId " + n + " does no longer exist -- apparent wraparound"}, "execute", n)
	}
}
