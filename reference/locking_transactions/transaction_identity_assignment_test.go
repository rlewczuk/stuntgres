package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIdentityAssignment(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, observer *sql.Conn) {
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_identity_assignment(id INT PRIMARY KEY,v INT)", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO tx_identity_assignment VALUES(1,0)", 1)
		held := make(map[string]*sql.Stmt)
		for _, name := range []string{"pg_current_xact_id", "pg_current_xact_id_if_assigned"} {
			stmt, err := a.PrepareContext(p.Ctx, "SELECT "+name+"()::text")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held[name] = stmt
		}
		for range 2 {
			fixture.LockExec(t, p.Ctx, a, "BEGIN READ ONLY", 0)
			identityUnassigned(t, p, a)
			var nullable sql.NullString
			if err := held["pg_current_xact_id_if_assigned"].QueryRowContext(p.Ctx).Scan(&nullable); err != nil || nullable.Valid {
				t.Fatalf("held inquiry assigned: %v %v", nullable, err)
			}
			identityBoth(t, p, b, a, fixture.Select("SELECT age(NULL::xid) AS a,mxid_age(NULL::xid) AS m,pg_xact_status(NULL::xid8) AS s", []string{"a", "m", "s"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{nil, nil, nil}}))
			identityBoth(t, p, b, a, fixture.Select("SELECT * FROM pg_get_multixact_members(NULL::xid)", []string{"xid", "mode"}, []string{"XID", "TEXT"}, nil))
			identityBoth(t, p, b, a, fixture.Select("SELECT 1 AS v,age('0'::xid) AS a,mxid_age('0'::xid) AS m,pg_xact_status('1'::xid8) AS s", []string{"v", "a", "m", "s"}, []string{"INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2147483647), int64(2147483647), "committed"}}))
			identityUnassigned(t, p, a)
			fixture.LockExec(t, p.Ctx, a, "SAVEPOINT first_id", 0)
			var id string
			if err := held["pg_current_xact_id"].QueryRowContext(p.Ctx).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if identityID(t, p, a) != id {
				t.Fatal("current identity changed within transaction")
			}
			identityStatus(t, p, observer, id, "in progress")
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK TO first_id", 0)
			fixture.LockExec(t, p.Ctx, a, "RELEASE first_id", 0)
			if err := held["pg_current_xact_id_if_assigned"].QueryRowContext(p.Ctx).Scan(&nullable); err != nil || !nullable.Valid || nullable.String != id {
				t.Fatalf("lost top identity: %v %v", nullable, err)
			}
			fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
			identityStatus(t, p, observer, id, "committed")
		}
		for _, q := range []string{
			"UPDATE tx_identity_assignment SET v=1 WHERE id=99",
			"DELETE FROM tx_identity_assignment WHERE id=99",
		} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, q, 0)
			identityUnassigned(t, p, a)
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		identityBoth(t, p, b, a, fixture.Select("SELECT id FROM tx_identity_assignment WHERE id=99 FOR UPDATE", []string{"id"}, []string{"INT4"}, nil))
		identityUnassigned(t, p, a)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		copyConn := a
		if !b.PG {
			copyConn, _ = fixture.LockConnections(t, p.Ctx, p.Native)
		}
		fixture.LockExec(t, p.Ctx, copyConn, "BEGIN", 0)
		identityUnassigned(t, p, copyConn)
		if b.PG {
			copyStmt, err := copyConn.PrepareContext(p.Ctx, "COPY tx_identity_assignment (id,v) FROM STDIN")
			if err != nil {
				t.Fatal(err)
			}
			_, err = copyStmt.ExecContext(p.Ctx)
			copyStmt.Close()
			if err != nil {
				t.Fatal(err)
			}
		} else {
			result, err := stuntgres.CopyFrom(p.Ctx, copyConn, "COPY tx_identity_assignment (id,v) FROM STDIN", strings.NewReader(""))
			if err != nil || result != (stuntgres.CopyResult{Count: 0, CommandTag: "COPY 0"}) {
				t.Fatalf("empty COPY: %+v %v", result, err)
			}
		}
		identityUnassigned(t, p, copyConn)
		fixture.LockExec(t, p.Ctx, copyConn, "ROLLBACK", 0)
		for _, q := range []string{"INSERT INTO tx_identity_assignment VALUES(2,0)", "UPDATE tx_identity_assignment SET v=v+1 WHERE id=1", "DELETE FROM tx_identity_assignment WHERE id=1"} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, a, "SAVEPOINT write", 0)
			fixture.LockExec(t, p.Ctx, a, q, 1)
			var id string
			if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id_if_assigned()::text").Scan(&id); err != nil {
				t.Fatal(err)
			}
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK TO write", 0)
			if identityID(t, p, a) != id {
				t.Fatal("savepoint erased write assignment")
			}
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
			identityStatus(t, p, observer, id, "aborted")
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		identityLock(t, p, a, "tx_identity_assignment", 1, "KEY SHARE")
		var id sql.NullString
		if err := a.QueryRowContext(p.Ctx, "SELECT pg_current_xact_id_if_assigned()::text").Scan(&id); err != nil || !id.Valid {
			t.Fatalf("physical row lock did not assign %v %v", id, err)
		}
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
	})
}

func TestReferenceTransactionIdentityAges(t *testing.T) {
	identityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		identityBoth(t, p, b, a, fixture.Select("SELECT age('0'::xid) AS a,age('1'::xid) AS b,age('2'::xid) AS c,mxid_age('0'::xid) AS m", []string{"a", "b", "c", "m"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(2147483647), int64(2147483647), int64(2147483647), int64(2147483647)}}))
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		var anchor int32
		if err := a.QueryRowContext(p.Ctx, "SELECT age('3'::xid)").Scan(&anchor); err != nil {
			t.Fatal(err)
		}
		identityUnassigned(t, p, a)
		identityID(t, p, peer)
		id := identityID(t, p, a)
		var again int32
		if err := a.QueryRowContext(p.Ctx, "SELECT age('3'::xid)").Scan(&again); err != nil || again != anchor {
			t.Fatalf("age anchor changed %d -> %d (%v)", anchor, again, err)
		}
		low, _ := strconv.ParseUint(identityLow(t, id), 10, 32)
		for _, x := range []uint32{3, 2147483648, 4294967295} {
			// Independent signed modular subtraction against the first demanded anchor.
			want := int64(int32(uint32(anchor) + 3 - x))
			identityBoth(t, p, b, a, fixture.Select("SELECT age($1::xid) AS a", []string{"a"}, []string{"INT4"}, [][]any{{want}}, strconv.FormatUint(uint64(x), 10)))
		}
		if low < 3 {
			t.Fatal("reserved allocated xid")
		}
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		id = identityID(t, p, a)
		identityBoth(t, p, b, a, fixture.Select("SELECT age($1::xid) AS a", []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}, identityLow(t, id)))
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		next := identityNextMulti(t, p, a)
		identityBoth(t, p, b, a, fixture.Select("SELECT mxid_age('1'::xid) AS a,mxid_age('2'::xid) AS b", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(int32(next - 1)), int64(int32(next - 2))}}))
	})
}
