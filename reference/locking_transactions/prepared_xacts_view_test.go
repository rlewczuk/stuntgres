package reference_test

import (
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePreparedXactsViewPublicVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"prepared_view_owner", "prepared_view_reader"})
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		for _, role := range []string{"prepared_view_owner", "prepared_view_reader"} {
			fixture.LockExec(t, p.Ctx, admin, "CREATE USER "+role, 0)
			if b.PG {
				fixture.LockExec(t, p.Ctx, admin, "ALTER ROLE "+role+" PASSWORD 'role-core-fixture'", 0)
			}
		}
		driver := p.NativeConnector.Driver().(*stuntgres.Driver)
		dsn := "reference/" + t.Name()[:strings.LastIndex(t.Name(), "/")]
		ownerPool := fixture.RoleScenarioLoginPool(t, driver, b.PG, dsn, "prepared_view_owner")
		readerPool := fixture.RoleScenarioLoginPool(t, driver, b.PG, dsn, "prepared_view_reader")
		owner, ownerPeer := fixture.LockConnections(t, p.Ctx, ownerPool)
		reader := fixture.SequenceCoreConn(t, p.Ctx, readerPool)
		var database, ownerName, readerName string
		if err := owner.QueryRowContext(p.Ctx, "SELECT current_database(), current_user").Scan(&database, &ownerName); err != nil {
			t.Fatal(err)
		}
		if err := reader.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&readerName); err != nil {
			t.Fatal(err)
		}
		if ownerName != "prepared_view_owner" || readerName != "prepared_view_reader" || ownerName == readerName {
			t.Fatal("unexpected login identities", ownerName, readerName)
		}
		const gid = "prepared_xacts_view_public"
		const query = "SELECT * FROM pg_catalog.pg_prepared_xacts WHERE gid=$1"
		held := fixture.IdentityHeld(t, p.Ctx, reader, query)
		if rows := fixture.PreparedReferenceQuery(t, p, held, query, gid); len(rows) != 0 {
			t.Fatal("SELECT preparation manufactured an entry", rows)
		}
		fixture.LockExec(t, p.Ctx, owner, "BEGIN", 0)
		fixture.PreparedReferencePrepare(t, p, b, owner, gid)
		rows := fixture.PreparedReferenceQuery(t, p, reader, query, gid)
		if len(rows) != 1 {
			t.Fatal("PUBLIC reader entry count", rows)
		}
		entry := rows[0]
		if entry.Gid != gid || !entry.Owner.Valid || entry.Owner.String != ownerName || entry.Database != database || entry.Prepared.IsZero() {
			t.Fatal("incomplete PUBLIC metadata", entry, ownerName, database)
		}
		if got := fixture.PreparedReferenceQuery(t, p, held, query, gid); !reflect.DeepEqual(got, rows) {
			t.Fatal("held PUBLIC metadata", got, rows)
		}
		if got := fixture.PreparedReferenceQuery(t, p, reader, "SELECT * FROM pg_prepared_xacts WHERE gid=$1", gid); !reflect.DeepEqual(got, rows) {
			t.Fatal("ordinary catalog lookup metadata", got, rows)
		}
		result, err := reader.ExecContext(p.Ctx, "COMMIT PREPARED "+fixture.PreparedReferenceLiteral(gid))
		if result != nil {
			t.Fatal("denied completion returned result")
		}
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied to finish prepared transaction")
		want.Hint = "Must be superuser or the user that prepared the transaction."
		fixture.AssertDiagnostic(t, err, &want)
		for _, x := range []fixture.Executor{reader, held} {
			if got := fixture.PreparedReferenceQuery(t, p, x, query, gid); !reflect.DeepEqual(got, rows) {
				t.Fatal("denied completion changed metadata", got, rows)
			}
		}
		fixture.LockExec(t, p.Ctx, ownerPeer, "ROLLBACK PREPARED "+fixture.PreparedReferenceLiteral(gid), 0)
		for _, x := range []fixture.Executor{reader, held} {
			if got := fixture.PreparedReferenceQuery(t, p, x, query, gid); len(got) != 0 {
				t.Fatal("completed entry remains visible", got)
			}
		}
	})
}

func TestReferencePreparedXactsViewFreshnessAndClock(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		origin, observer := fixture.LockConnections(t, p.Ctx, b.DB)
		finisher := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, observer, "CREATE TABLE prepared_xacts_view_data(v INT)", 0)
		var database, owner string
		if err := origin.QueryRowContext(p.Ctx, "SELECT current_database(), current_user").Scan(&database, &owner); err != nil {
			t.Fatal(err)
		}
		const firstGID = "prepared_xacts_view_a"
		const secondGID = "prepared_xacts_view_b"
		const query = "SELECT * FROM pg_catalog.pg_prepared_xacts WHERE gid IN ($1,$2) ORDER BY gid"
		held := fixture.IdentityHeld(t, p.Ctx, observer, query)
		// Every phase executes this same statement as well as an independent direct query.
		assertRows := func(want any) {
			t.Helper()
			for _, x := range []fixture.Executor{observer, held} {
				if got := fixture.PreparedReferenceQuery(t, p, x, query, firstGID, secondGID); !reflect.DeepEqual(got, want) {
					t.Fatal("prepared view phase", got, want)
				}
			}
		}
		empty := fixture.PreparedReferenceQuery(t, p, observer, query, firstGID, secondGID)
		if len(empty) != 0 {
			t.Fatal("unexpected initial entries", empty)
		}
		assertRows(empty)
		// Bounds are server instants, not client time or the transaction-start clock.
		prepare := func(gid string) (time.Time, time.Time) {
			t.Helper()
			var start time.Time
			if err := origin.QueryRowContext(p.Ctx, "SELECT CURRENT_TIMESTAMP").Scan(&start); err != nil {
				t.Fatal(err)
			}
			start = start.Truncate(time.Microsecond)
			lower := commitTimeClock(t, p, origin).Truncate(time.Microsecond)
			for !lower.After(start) {
				lower = commitTimeClock(t, p, origin).Truncate(time.Microsecond)
			}
			fixture.PreparedReferencePrepare(t, p, b, origin, gid)
			return lower, commitTimeClock(t, p, finisher).Truncate(time.Microsecond)
		}
		fixture.LockExec(t, p.Ctx, origin, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, origin, "INSERT INTO prepared_xacts_view_data VALUES(7)", 1)
		assertRows(empty)
		lower, upper := prepare(firstGID)
		first := fixture.PreparedReferenceQuery(t, p, observer, query, firstGID, secondGID)
		if len(first) != 1 {
			t.Fatal("first prepared entry count", first)
		}
		a := first[0]
		if a.Gid != firstGID || !a.Owner.Valid || a.Owner.String != owner || a.Database != database || a.Prepared.Before(lower) || a.Prepared.After(upper) {
			t.Fatal("first prepared facts or clock", a, owner, database, lower, upper)
		}
		assertRows(first)
		fixture.LockExec(t, p.Ctx, origin, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, origin, "INSERT INTO prepared_xacts_view_data VALUES(9)", 1)
		assertRows(first)
		lower, upper = prepare(secondGID)
		both := fixture.PreparedReferenceQuery(t, p, observer, query, firstGID, secondGID)
		if len(both) != 2 || !reflect.DeepEqual(both[0], a) {
			t.Fatal("two-entry projection", both, a)
		}
		second := both[1]
		if second.Gid != secondGID || !second.Owner.Valid || second.Owner.String != owner || second.Database != database || second.Xid == a.Xid || second.Prepared.Before(lower) || second.Prepared.After(upper) {
			t.Fatal("second prepared facts or clock", second, a, owner, database, lower, upper)
		}
		assertRows(both)
		fixture.LockExec(t, p.Ctx, finisher, "COMMIT PREPARED "+fixture.PreparedReferenceLiteral(firstGID), 0)
		assertRows(both[1:])
		fixture.PreparedReferenceRows(t, p, b, finisher, "SELECT v FROM prepared_xacts_view_data ORDER BY v", 7)
		fixture.LockExec(t, p.Ctx, finisher, "ROLLBACK PREPARED "+fixture.PreparedReferenceLiteral(secondGID), 0)
		assertRows(empty)
		fixture.PreparedReferenceRows(t, p, b, finisher, "SELECT v FROM prepared_xacts_view_data ORDER BY v", 7)
	})
}

func TestReferencePreparedXactsViewFailedPrepare(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		origin, observer := fixture.LockConnections(t, p.Ctx, b.DB)
		finisher := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, observer, "CREATE TABLE prepared_xacts_view_failed(v INT)", 0)
		const sentinelGID = "prepared_xacts_view_sentinel"
		const refusedGID = "prepared_xacts_view_refused"
		fixture.LockExec(t, p.Ctx, finisher, "BEGIN", 0)
		fixture.PreparedReferencePrepare(t, p, b, finisher, sentinelGID)
		sentinel := fixture.PreparedReferenceOne(t, p, observer, sentinelGID)
		const query = "SELECT * FROM pg_catalog.pg_prepared_xacts WHERE gid=$1"
		held := fixture.IdentityHeld(t, p.Ctx, observer, query)
		assertAbsent := func() {
			t.Helper()
			for _, x := range []fixture.Executor{observer, held} {
				if got := fixture.PreparedReferenceQuery(t, p, x, query, refusedGID); len(got) != 0 {
					t.Fatal("refused or unprepared transaction projected", got)
				}
			}
		}
		assertAbsent()
		fixture.LockExec(t, p.Ctx, origin, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, origin, "INSERT INTO prepared_xacts_view_failed VALUES(13)", 1)
		fixture.LockExec(t, p.Ctx, origin, "CREATE TEMP SEQUENCE prepared_xacts_view_temp", 0)
		assertAbsent()
		fixture.PreparedReferenceCleanup(t, b, refusedGID)
		fixture.PreparedReferenceError(t, p.Ctx, origin, "PREPARE TRANSACTION "+fixture.PreparedReferenceLiteral(refusedGID), "0A000", "cannot PREPARE a transaction that has operated on temporary objects")
		assertAbsent()
		if got := fixture.PreparedReferenceOne(t, p, observer, sentinelGID); !reflect.DeepEqual(got, sentinel) {
			t.Fatal("failed prepare changed independent entry", got, sentinel)
		}
		fixture.PreparedReferenceRows(t, p, b, observer, "SELECT v FROM prepared_xacts_view_failed ORDER BY v")
		fixture.PreparedReferenceRows(t, p, b, origin, "SELECT 1 AS v", 1)
		fixture.LockExec(t, p.Ctx, finisher, "ROLLBACK PREPARED "+fixture.PreparedReferenceLiteral(sentinelGID), 0)
		fixture.PreparedReferenceAbsent(t, p, observer, sentinelGID)
	})
}
