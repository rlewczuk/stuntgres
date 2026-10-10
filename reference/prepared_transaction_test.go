package reference_test

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReferencePreparedTransactionFeatureSmoke(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, peer, "CREATE TABLE prepared_lifecycle(v INT)", 0)
		var database, owner string
		if err := a.QueryRowContext(p.ctx, "SELECT current_database(), current_user").Scan(&database, &owner); err != nil {
			t.Fatal(err)
		}
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_lifecycle VALUES(7)", 1)
		before := time.Now().Add(-time.Second)
		preparedReferencePrepare(t, p, b, a, "prepared_commit")
		entry := preparedReferenceOne(t, p, peer, "prepared_commit")
		if entry.gid != "prepared_commit" || !entry.owner.Valid || entry.owner.String != owner || entry.database != database || entry.prepared.Before(before) || entry.prepared.After(time.Now().Add(time.Second)) {
			t.Fatal(entry, database, owner)
		}
		if again := preparedReferenceOne(t, p, a, entry.gid); !reflect.DeepEqual(entry, again) {
			t.Fatal(entry, again)
		}
		preparedReferenceRows(t, p, b, a, "SELECT v FROM prepared_lifecycle ORDER BY v")
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_lifecycle ORDER BY v")
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		referenceLockExec(t, p.ctx, a, "ROLLBACK", 0)
		// Disable idle retention before returning the borrower: this closes the
		// physical origin rather than merely returning sql.Conn to its pool.
		b.db.SetMaxIdleConns(0)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		if got := preparedReferenceOne(t, p, peer, entry.gid); !reflect.DeepEqual(entry, got) {
			t.Fatal(entry, got)
		}
		referenceLockExec(t, p.ctx, peer, "COMMIT PREPARED 'prepared_commit'", 0)
		preparedReferenceAbsent(t, p, peer, entry.gid)
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_lifecycle ORDER BY v", 7)
		origin := sequenceCoreConn(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, origin, "BEGIN", 0)
		referenceLockExec(t, p.ctx, origin, "INSERT INTO prepared_lifecycle VALUES(9)", 1)
		preparedReferencePrepare(t, p, b, origin, "prepared_rollback")
		second := preparedReferenceOne(t, p, peer, "prepared_rollback")
		if second.xid == entry.xid {
			t.Fatal("reused transaction identity")
		}
		origin.Close()
		referenceLockExec(t, p.ctx, peer, "ROLLBACK PREPARED 'prepared_rollback'", 0)
		preparedReferenceAbsent(t, p, peer, second.gid)
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_lifecycle ORDER BY v", 7)
		for _, gid := range []string{entry.gid, second.gid} {
			referenceLockExec(t, p.ctx, peer, "BEGIN", 0)
			preparedReferencePrepare(t, p, b, peer, gid)
			referenceLockExec(t, p.ctx, peer, "ROLLBACK PREPARED "+preparedReferenceLiteral(gid), 0)
		}
	})
}

func TestReferencePreparedTransactionGIDs(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, peer, "CREATE TABLE prepared_gid(v INT)", 0)
		for _, gid := range []string{"", "Case", "case", "quote'\"\\", "é", strings.Repeat("x", 199), strings.Repeat("é", 99) + "x"} {
			referenceLockExec(t, p.ctx, a, "BEGIN", 0)
			preparedReferencePrepare(t, p, b, a, gid)
			original := preparedReferenceOne(t, p, peer, gid)
			referenceLockExec(t, p.ctx, peer, "BEGIN", 0)
			referenceLockExec(t, p.ctx, peer, "INSERT INTO prepared_gid VALUES(1)", 1)
			referenceLockExec(t, p.ctx, peer, "SAVEPOINT child", 0)
			preparedReferenceError(t, p.ctx, peer, "PREPARE TRANSACTION "+preparedReferenceLiteral(gid), "42710", "transaction identifier \""+gid+"\" is already in use")
			if got := preparedReferenceOne(t, p, peer, gid); !reflect.DeepEqual(original, got) {
				t.Fatal(original, got)
			}
			// Failure ends the full transaction; this new INSERT is autonomous.
			referenceLockExec(t, p.ctx, peer, "INSERT INTO prepared_gid VALUES(2)", 1)
			preparedReferenceRows(t, p, b, a, "SELECT v FROM prepared_gid", 2)
			referenceLockExec(t, p.ctx, peer, "DELETE FROM prepared_gid", 1)
			referenceLockExec(t, p.ctx, peer, "ROLLBACK PREPARED "+preparedReferenceLiteral(gid), 0)
		}
		for _, gid := range []string{strings.Repeat("x", 200), strings.Repeat("é", 100)} {
			preparedReferenceCleanup(t, b, gid)
			referenceLockExec(t, p.ctx, a, "BEGIN", 0)
			preparedReferenceError(t, p.ctx, a, "PREPARE TRANSACTION "+preparedReferenceLiteral(gid), "22023", "transaction identifier \""+gid+"\" is too long")
			preparedReferenceAbsent(t, p, peer, gid)
		}
		for _, command := range []string{"COMMIT", "ROLLBACK"} {
			for _, gid := range []string{"absent", strings.Repeat("x", 200)} {
				preparedReferenceError(t, p.ctx, peer, command+" PREPARED "+preparedReferenceLiteral(gid), "42704", "prepared transaction with identifier \""+gid+"\" does not exist")
			}
		}
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		preparedReferencePrepare(t, p, b, a, "CASE")
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		preparedReferencePrepare(t, p, b, a, "case")
		for _, gid := range []string{"CASE", "case"} {
			preparedReferenceOne(t, p, peer, gid)
			referenceLockExec(t, p.ctx, peer, "COMMIT PREPARED "+preparedReferenceLiteral(gid), 0)
		}
	})
}

func TestReferencePreparedTransactionStateAndHeldUtilities(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		f := newControlsReferenceFixture(t, p, b.pg)
		b.db = f.db
		f.exec(t, f.b, "CREATE TABLE t(v INT NOT NULL)", false, 0)
		preparedReferenceCleanup(t, b, "held_prepare")
		held, err := f.a.PrepareContext(p.ctx, "PREPARE TRANSACTION 'held_prepare'")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		f.noticesWant(t, "", "")
		f.exec(t, f.a, "BEGIN", false, 0)
		f.exec(t, f.a, "INSERT INTO t VALUES(1)", false, 1)
		preparedReferenceAbsent(t, p, f.b, "held_prepare")
		f.rows(t, f.a, 1)
		f.rows(t, f.b)
		f.exec(t, f.a, "SAVEPOINT child", false, 0)
		if r, err := held.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		} else {
			referenceLockAffected(t, r, 0)
		}
		f.rows(t, f.a)
		preparedReferenceOne(t, p, f.b, "held_prepare")
		end, err := f.b.PrepareContext(p.ctx, "COMMIT PREPARED 'held_prepare'")
		if err != nil {
			t.Fatal(err)
		}
		defer end.Close()
		preparedReferenceOne(t, p, f.b, "held_prepare")
		if r, err := end.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		} else {
			referenceLockAffected(t, r, 0)
		}
		f.rows(t, f.b, 1)
		if r, err := held.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		} else {
			referenceLockAffected(t, r, 0)
		}
		f.noticesWant(t, "25P01", "there is no transaction in progress")
		preparedReferenceAbsent(t, p, f.b, "held_prepare")
		f.exec(t, f.a, "BEGIN", false, 0)
		want := referenceTransactionNull()
		referenceAssertDiagnostic(t, transactionConstraintExec(t, p.ctx, f.a, "INSERT INTO t VALUES(NULL)", false), &want)
		if r, err := held.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		} else {
			referenceLockAffected(t, r, 0)
		}
		f.noticesWant(t, "", "")
		preparedReferenceAbsent(t, p, f.b, "held_prepare")
		for _, word := range []string{"COMMIT", "ROLLBACK"} {
			f.exec(t, f.a, "BEGIN", false, 0)
			preparedReferenceError(t, p.ctx, f.a, word+" PREPARED 'missing'", "25001", word+" PREPARED cannot run inside a transaction block")
			preparedReferenceError(t, p.ctx, f.a, word+" PREPARED 'missing'", "25P02", "current transaction is aborted, commands ignored until end of transaction block")
			f.exec(t, f.a, "ROLLBACK", false, 0)
		}
		f.exec(t, f.a, "BEGIN READ ONLY", false, 0)
		preparedReferencePrepare(t, p, b, f.a, "read_only")
		f.exec(t, f.b, "SET default_transaction_read_only=on", false, 0)
		f.exec(t, f.b, "COMMIT PREPARED 'read_only'", false, 0)
		f.exec(t, f.b, "SET default_transaction_read_only=off", false, 0)
		rows, err := f.a.QueryContext(p.ctx, "BEGIN")
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		preparedReferenceCleanup(t, b, "query_control")
		rows, err = f.a.QueryContext(p.ctx, "PREPARE TRANSACTION 'query_control'")
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil || len(cols) != 0 || rows.Next() {
			t.Fatal(cols, err)
		}
		rows.Close()
		f.exec(t, f.b, "ROLLBACK PREPARED 'query_control'", false, 0)
	})
}
