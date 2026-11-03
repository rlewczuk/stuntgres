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

func checkpointerCluster(t *testing.T) {
	names := []string{"checkpointer_existing", "checkpointer_late_first", "checkpointer_late_second"}
	fixture.DatabaseReferenceCleanup(t, &names)
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE checkpointer_existing", 0)
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, names[0], "")
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
		for _, issuer := range []*sql.Conn{c, peer} {
			before := checkpointerGet(t, p, b, c)
			lower := checkpointerClock(t, p, issuer)
			checkpointerVoid(t, p, b, issuer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
			after := checkpointerGet(t, p, b, c)
			checkpointerEvent(t, before, after, lower, checkpointerClock(t, p, issuer))
			checkpointerSameReset(t, after, checkpointerGet(t, p, b, peer))
		}
		before := checkpointerGet(t, p, b, c)
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
				checkpointerSameReset(t, before, checkpointerGet(t, p, b, connection))
			}
			if i == 0 {
				// A late target inherits current authority and can advance it
				// for existing targets and the next distinct publication.
				lower := checkpointerClock(t, p, late)
				checkpointerVoid(t, p, b, late, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
				after := checkpointerGet(t, p, b, late)
				checkpointerEvent(t, before, after, lower, checkpointerClock(t, p, late))
				if !after.reset.After(before.reset) {
					t.Fatal("late database reset did not advance cluster authority", before, after)
				}
				for _, connection := range connections {
					checkpointerSameReset(t, after, checkpointerGet(t, p, b, connection))
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
			initial := checkpointerGet(t, p, b, other)
			checkpointerVoid(t, p, b, c, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
			checkpointerSameReset(t, initial, checkpointerGet(t, p, b, other))
			current := checkpointerGet(t, p, b, c)
			checkpointerVoid(t, p, b, other, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
			checkpointerSameReset(t, current, checkpointerGet(t, p, b, c))
		}
	})
}

func checkpointerNativeControls(t *testing.T) {
	names := []string{"checkpointer_control_peer"}
	fixture.DatabaseReferenceCleanup(t, &names)
	var emitted []byte
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			before := checkpointerGet(t, p, b, c)
			if len(emitted) == 0 {
				t.Fatal("missing actual emitted SQL")
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("oracle replay", err)
			}
			checkpointerSentinel(t, p, b, c)
			checkpointerSameReset(t, before, checkpointerGet(t, p, b, c))
			return
		}
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE checkpointer_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO checkpointer_sentinel VALUES(37)", 1)
		fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE checkpointer_control_peer", 0)
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, false, names[0], "")
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
		checkpointerVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
		before := checkpointerGet(t, p, b, c)
		held := checkpointerHeld(t, p, c, checkpointerSQL)
		defer held.Statement.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		if len(emitted) == 0 || strings.Contains(dump.String(), "stats_reset") || strings.Contains(dump.String(), "pg_stat_checkpointer") {
			t.Fatal("dump must serialize user data, never monitoring history", dump.String())
		}
		check := func() {
			t.Helper()
			checkpointerSameReset(t, before, checkpointerGet(t, p, b, held))
			checkpointerSameReset(t, before, checkpointerGet(t, p, b, peer))
			checkpointerSentinel(t, p, b, c)
		}
		check()
		bad := append(bytes.Clone(emitted), []byte("\nCREATE TABLE late_failure(v absent_type);")...)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(bad)); err == nil {
			t.Fatal("late failed Restore accepted")
		}
		check()
		readErr := errors.New("checkpointer input failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(emitted), checkpointerErrorReader{readErr})); !errors.Is(err, readErr) {
			t.Fatal("external reader failure", err)
		}
		check()
		if err := stuntgres.Dump(p.Ctx, p.Native, checkpointerErrorWriter{readErr}); !errors.Is(err, readErr) {
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
		input := &checkpointerCancelReader{Reader: bytes.NewReader(emitted), cancel: cancelInput}
		if err := stuntgres.Restore(inputCtx, p.Native, input); !errors.Is(err, context.Canceled) || !input.entered {
			t.Fatal("cancellation after external input admission", err)
		}
		cancelInput()
		check()
		for _, restore := range []bool{false, true} {
			retained, err := held.QueryContext(p.Ctx, checkpointerSQL)
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
			checkpointerSameReset(t, before, checkpointerRead(t, retained, false))
			checkpointerSameReset(t, before, checkpointerGet(t, p, b, held))
			checkpointerSameReset(t, before, checkpointerGet(t, p, b, peer))
			if restore {
				checkpointerSentinel(t, p, b, c)
			} else {
				want := checkpointerDiagnostic("42P01", `relation "checkpointer_sentinel" does not exist`)
				want.Position = fixture.Position(15)
				step := fixture.ErrorDiagnostic("SELECT v FROM checkpointer_sentinel", want)
				step.SelectRows, step.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			}
		}
		// A separate native destination keeps its own genuine reset on replay.
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("checkpointer_dump_destination")
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
		initial := checkpointerGet(t, p, b, dest)
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		checkpointerSameReset(t, initial, checkpointerGet(t, p, b, dest))
		checkpointerSentinel(t, p, b, dest)
		// Retain actual Rows across a peer reset, not just a copied row struct.
		retained, err := held.QueryContext(p.Ctx, checkpointerSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		checkpointerVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
		checkpointerSameReset(t, before, checkpointerRead(t, retained, false))
		if current := checkpointerGet(t, p, b, held); !current.reset.After(before.reset) {
			t.Fatal("held reexecution did not acquire current cluster authority")
		}
	})
}

type checkpointerErrorReader struct{ err error }

func (r checkpointerErrorReader) Read([]byte) (int, error) { return 0, r.err }

type checkpointerCancelReader struct {
	io.Reader
	cancel  context.CancelFunc
	entered bool
}

func (r *checkpointerCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if n > 0 && !r.entered {
		r.entered = true
		r.cancel()
	}
	return n, err
}

type checkpointerErrorWriter struct{ err error }

func (w checkpointerErrorWriter) Write([]byte) (int, error) { return 0, w.err }
func TestReferencePGStatCheckpointerLifetime(t *testing.T) {
	t.Run("absent activity", checkpointerAbsentActivity)
	t.Run("cluster", checkpointerCluster)
	t.Run("native controls", checkpointerNativeControls)
}
