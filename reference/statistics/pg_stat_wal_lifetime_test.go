package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatWALCluster(t *testing.T) {
	names := []string{"wal_existing", "wal_late_first", "wal_late_second"}
	fixture.DatabaseReferenceCleanup(t, &names)
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE wal_existing", 0)
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, names[0], "")
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
		for _, issuer := range []*sql.Conn{c, peer} {
			before := walGet(t, p, b, c)
			lower := archiverClock(t, p, issuer)
			archiverVoid(t, p, b, issuer, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
			after := walGet(t, p, b, c)
			walEvent(t, before, after, lower, archiverClock(t, p, issuer))
			walSame(t, after, walGet(t, p, b, peer))
		}
		before := walGet(t, p, b, c)
		connections := []*sql.Conn{c, peer}
		for i, name := range names[1:] {
			fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE "+name, 0)
			latePool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, name, "")
			late, err := latePool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer late.Close()
			fixture.StatisticsExec(t, p, b, late, "SET stats_fetch_consistency=none", 0)
			connections = append(connections, late)
			for _, connection := range connections {
				walSame(t, before, walGet(t, p, b, connection))
			}
			if i == 0 {
				// A late target inherits current authority and can advance it
				// for existing targets and the next distinct publication.
				lower := archiverClock(t, p, late)
				archiverVoid(t, p, b, late, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
				after := walGet(t, p, b, late)
				walEvent(t, before, after, lower, archiverClock(t, p, late))
				if !after.reset.After(before.reset) {
					t.Fatal("late database reset did not advance cluster authority", before, after)
				}
				for _, connection := range connections {
					walSame(t, after, walGet(t, p, b, connection))
				}
				before = after
			}
		}
		if !b.PG {
			// The same opaque DSN is not cluster authority across Driver objects.
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector(p.NativeDSN)
			if err != nil {
				t.Fatal(err)
			}
			isolated := sql.OpenDB(connector)
			defer isolated.Close()
			other, err := isolated.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer other.Close()
			initial := walGet(t, p, b, other)
			archiverVoid(t, p, b, c, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
			walSame(t, initial, walGet(t, p, b, other))
			current := walGet(t, p, b, c)
			archiverVoid(t, p, b, other, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
			walSame(t, current, walGet(t, p, b, c))
		}
	})
}

func TestReferencePGStatWALNativeControls(t *testing.T) {
	names := []string{"wal_control_peer"}
	fixture.DatabaseReferenceCleanup(t, &names)
	var emitted []byte
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			before := walGet(t, p, b, c)
			if len(emitted) == 0 {
				t.Fatal("missing actual emitted SQL")
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("oracle replay", err)
			}
			walSentinel(t, p, b, c)
			walSame(t, before, walGet(t, p, b, c))
			return
		}
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE wal_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO wal_sentinel VALUES(37)", 1)
		fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE wal_control_peer", 0)
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, false, names[0], "")
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
		archiverVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
		before := walGet(t, p, b, c)
		held := archiverHeld(t, p, c, walSQL)
		defer held.Statement.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if len(emitted) == 0 || strings.Contains(dump.String(), "stats_reset") || strings.Contains(dump.String(), "pg_stat_wal") {
			t.Fatal("dump must serialize user data, never monitoring history", dump.String())
		}
		check := func() {
			t.Helper()
			walSame(t, before, walGet(t, p, b, held))
			walSame(t, before, walGet(t, p, b, peer))
			walSentinel(t, p, b, c)
		}
		check()
		bad := append(bytes.Clone(emitted), []byte("\nCREATE TABLE late_failure(v absent_type);")...)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
			t.Fatal("late failed Restore accepted")
		}
		check()
		readErr := errors.New("wal input failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(emitted), archiverErrorReader{readErr})); !errors.Is(err, readErr) {
			t.Fatal("external reader failure", err)
		}
		check()
		if err := stuntgres.Dump(p.Ctx, p.Native, archiverErrorWriter{readErr}); !errors.Is(err, readErr) {
			t.Fatal("external Dump writer failure", err)
		}
		check()
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		for _, control := range []func() error{
			func() error { return stuntgres.Reset(canceled, p.Native) },
			func() error { return stuntgres.Restore(canceled, p.Native, bytes.NewReader(emitted)) },
			func() error { return stuntgres.Dump(canceled, p.Native, io.Discard) },
		} {
			if err := control(); !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary cancellation", err)
			}
			check()
		}
		inputCtx, cancelInput := context.WithCancel(p.Ctx)
		input := &archiverCancelReader{Reader: bytes.NewReader(emitted), cancel: cancelInput}
		if err := stuntgres.Restore(inputCtx, p.Native, input); !errors.Is(err, context.Canceled) || !input.entered {
			t.Fatal("cancellation after external input admission", err)
		}
		cancelInput()
		check()
		for _, restore := range []bool{false, true} {
			retained, err := held.QueryContext(p.Ctx, walSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			if restore {
				err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted))
			} else {
				err = stuntgres.Reset(p.Ctx, p.Native)
			}
			if err != nil {
				t.Fatal(err)
			}
			walSame(t, before, walRead(t, retained, false))
			walSame(t, before, walGet(t, p, b, held))
			walSame(t, before, walGet(t, p, b, peer))
			if restore {
				walSentinel(t, p, b, c)
			}
		}
		// A separate native destination keeps its own genuine reset on replay.
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("wal_dump_destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		dest, err := destination.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dest.Close()
		initial := walGet(t, p, b, dest)
		if initial.reset.Equal(before.reset) {
			t.Fatal("independent destination requires a distinguishing genuine reset")
		}
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		walSame(t, initial, walGet(t, p, b, dest))
		walSentinel(t, p, b, dest)
		// Retain actual Rows across a peer reset, not just a copied row struct.
		retained, err := held.QueryContext(p.Ctx, walSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		archiverVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
		walSame(t, before, walRead(t, retained, false))
		if current := walGet(t, p, b, held); !current.reset.After(before.reset) {
			t.Fatal("held reexecution did not acquire current cluster authority")
		}
	})
}
