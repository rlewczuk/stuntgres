package reference_test

import (
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePreparedTransactionFeatureSmoke(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE prepared_lifecycle(v INT)", 0)
		var database, owner string
		if err := a.QueryRowContext(p.Ctx, "SELECT current_database(), current_user").Scan(&database, &owner); err != nil {
			t.Fatal(err)
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_lifecycle VALUES(7)", 1)
		before := time.Now().Add(-time.Second)
		fixture.PreparedReferencePrepare(t, p, b, a, "prepared_commit")
		entry := fixture.PreparedReferenceOne(t, p, peer, "prepared_commit")
		if entry.Gid != "prepared_commit" || !entry.Owner.Valid || entry.Owner.String != owner || entry.Database != database || entry.Prepared.Before(before) || entry.Prepared.After(time.Now().Add(time.Second)) {
			t.Fatal(entry, database, owner)
		}
		if again := fixture.PreparedReferenceOne(t, p, a, entry.Gid); !reflect.DeepEqual(entry, again) {
			t.Fatal(entry, again)
		}
		fixture.PreparedReferenceRows(t, p, b, a, "SELECT v FROM prepared_lifecycle ORDER BY v")
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_lifecycle ORDER BY v")
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		// Disable idle retention before returning the borrower: this closes the
		// physical origin rather than merely returning sql.Conn to its pool.
		b.DB.SetMaxIdleConns(0)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		if got := fixture.PreparedReferenceOne(t, p, peer, entry.Gid); !reflect.DeepEqual(entry, got) {
			t.Fatal(entry, got)
		}
		fixture.LockExec(t, p.Ctx, peer, "COMMIT PREPARED 'prepared_commit'", 0)
		fixture.PreparedReferenceAbsent(t, p, peer, entry.Gid)
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_lifecycle ORDER BY v", 7)
		origin := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, origin, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, origin, "INSERT INTO prepared_lifecycle VALUES(9)", 1)
		fixture.PreparedReferencePrepare(t, p, b, origin, "prepared_rollback")
		second := fixture.PreparedReferenceOne(t, p, peer, "prepared_rollback")
		if second.Xid == entry.Xid {
			t.Fatal("reused transaction identity")
		}
		origin.Close()
		fixture.LockExec(t, p.Ctx, peer, "ROLLBACK PREPARED 'prepared_rollback'", 0)
		fixture.PreparedReferenceAbsent(t, p, peer, second.Gid)
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_lifecycle ORDER BY v", 7)
		for _, gid := range []string{entry.Gid, second.Gid} {
			fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
			fixture.PreparedReferencePrepare(t, p, b, peer, gid)
			fixture.LockExec(t, p.Ctx, peer, "ROLLBACK PREPARED "+fixture.PreparedReferenceLiteral(gid), 0)
		}
	})
}

func TestReferencePreparedTransactionGIDs(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE prepared_gid(v INT)", 0)
		for _, gid := range []string{"", "Case", "case", "quote'\"\\", "é", strings.Repeat("x", 199), strings.Repeat("é", 99) + "x"} {
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.PreparedReferencePrepare(t, p, b, a, gid)
			original := fixture.PreparedReferenceOne(t, p, peer, gid)
			fixture.LockExec(t, p.Ctx, peer, "BEGIN", 0)
			fixture.LockExec(t, p.Ctx, peer, "INSERT INTO prepared_gid VALUES(1)", 1)
			fixture.LockExec(t, p.Ctx, peer, "SAVEPOINT child", 0)
			fixture.PreparedReferenceError(t, p.Ctx, peer, "PREPARE TRANSACTION "+fixture.PreparedReferenceLiteral(gid), "42710", "transaction identifier \""+gid+"\" is already in use")
			if got := fixture.PreparedReferenceOne(t, p, peer, gid); !reflect.DeepEqual(original, got) {
				t.Fatal(original, got)
			}
			// Failure ends the full transaction; this new INSERT is autonomous.
			fixture.LockExec(t, p.Ctx, peer, "INSERT INTO prepared_gid VALUES(2)", 1)
			fixture.PreparedReferenceRows(t, p, b, a, "SELECT v FROM prepared_gid", 2)
			fixture.LockExec(t, p.Ctx, peer, "DELETE FROM prepared_gid", 1)
			fixture.LockExec(t, p.Ctx, peer, "ROLLBACK PREPARED "+fixture.PreparedReferenceLiteral(gid), 0)
		}
		for _, gid := range []string{strings.Repeat("x", 200), strings.Repeat("é", 100)} {
			fixture.PreparedReferenceCleanup(t, b, gid)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			fixture.PreparedReferenceError(t, p.Ctx, a, "PREPARE TRANSACTION "+fixture.PreparedReferenceLiteral(gid), "22023", "transaction identifier \""+gid+"\" is too long")
			fixture.PreparedReferenceAbsent(t, p, peer, gid)
		}
		for _, command := range []string{"COMMIT", "ROLLBACK"} {
			for _, gid := range []string{"absent", strings.Repeat("x", 200)} {
				fixture.PreparedReferenceError(t, p.Ctx, peer, command+" PREPARED "+fixture.PreparedReferenceLiteral(gid), "42704", "prepared transaction with identifier \""+gid+"\" does not exist")
			}
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.PreparedReferencePrepare(t, p, b, a, "CASE")
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.PreparedReferencePrepare(t, p, b, a, "case")
		for _, gid := range []string{"CASE", "case"} {
			fixture.PreparedReferenceOne(t, p, peer, gid)
			fixture.LockExec(t, p.Ctx, peer, "COMMIT PREPARED "+fixture.PreparedReferenceLiteral(gid), 0)
		}
	})
}

func TestReferencePreparedTransactionStateAndHeldUtilities(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		f := fixture.NewControlsReferenceFixture(t, p, b.PG)
		b.DB = f.DB
		f.Exec(t, f.B, "CREATE TABLE t(v INT NOT NULL)", false, 0)
		fixture.PreparedReferenceCleanup(t, b, "held_prepare")
		held, err := f.A.PrepareContext(p.Ctx, "PREPARE TRANSACTION 'held_prepare'")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		f.NoticesWant(t, "", "")
		f.Exec(t, f.A, "BEGIN", false, 0)
		f.Exec(t, f.A, "INSERT INTO t VALUES(1)", false, 1)
		fixture.PreparedReferenceAbsent(t, p, f.B, "held_prepare")
		f.Rows(t, f.A, 1)
		f.Rows(t, f.B)
		f.Exec(t, f.A, "SAVEPOINT child", false, 0)
		if r, err := held.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		} else {
			fixture.LockAffected(t, r, 0)
		}
		f.Rows(t, f.A)
		fixture.PreparedReferenceOne(t, p, f.B, "held_prepare")
		end, err := f.B.PrepareContext(p.Ctx, "COMMIT PREPARED 'held_prepare'")
		if err != nil {
			t.Fatal(err)
		}
		defer end.Close()
		fixture.PreparedReferenceOne(t, p, f.B, "held_prepare")
		if r, err := end.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		} else {
			fixture.LockAffected(t, r, 0)
		}
		f.Rows(t, f.B, 1)
		if r, err := held.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		} else {
			fixture.LockAffected(t, r, 0)
		}
		f.NoticesWant(t, "25P01", "there is no transaction in progress")
		fixture.PreparedReferenceAbsent(t, p, f.B, "held_prepare")
		f.Exec(t, f.A, "BEGIN", false, 0)
		want := fixture.TransactionNull()
		fixture.AssertDiagnostic(t, fixture.TransactionConstraintExec(t, p.Ctx, f.A, "INSERT INTO t VALUES(NULL)", false), &want)
		if r, err := held.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		} else {
			fixture.LockAffected(t, r, 0)
		}
		f.NoticesWant(t, "", "")
		fixture.PreparedReferenceAbsent(t, p, f.B, "held_prepare")
		for _, word := range []string{"COMMIT", "ROLLBACK"} {
			f.Exec(t, f.A, "BEGIN", false, 0)
			fixture.PreparedReferenceError(t, p.Ctx, f.A, word+" PREPARED 'missing'", "25001", word+" PREPARED cannot run inside a transaction block")
			fixture.PreparedReferenceError(t, p.Ctx, f.A, word+" PREPARED 'missing'", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
			f.Exec(t, f.A, "ROLLBACK", false, 0)
		}
		f.Exec(t, f.A, "BEGIN READ ONLY", false, 0)
		fixture.PreparedReferencePrepare(t, p, b, f.A, "read_only")
		f.Exec(t, f.B, "SET default_transaction_read_only=on", false, 0)
		f.Exec(t, f.B, "COMMIT PREPARED 'read_only'", false, 0)
		f.Exec(t, f.B, "SET default_transaction_read_only=off", false, 0)
		rows, err := f.A.QueryContext(p.Ctx, "BEGIN")
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		fixture.PreparedReferenceCleanup(t, b, "query_control")
		rows, err = f.A.QueryContext(p.Ctx, "PREPARE TRANSACTION 'query_control'")
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil || len(cols) != 0 || rows.Next() {
			t.Fatal(cols, err)
		}
		rows.Close()
		f.Exec(t, f.B, "ROLLBACK PREPARED 'query_control'", false, 0)
	})
}
