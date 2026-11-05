package logical

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
)

const liveness = 10 * time.Second

// A visible unfinished slot precedes the immutable XID snapshot. Observe the
// creator's actual completion wait before assigning another transaction an XID.
func (f *fixture) waitCreationBoundary(creator *session.Session) {
	f.t.Helper()
	eventually(f.t, "the captured creation boundary", func() bool {
		records, err := f.e.Locks.PGLocks(f.e.Catalog.Domain, f.e.CatalogRead().State().Epoch())
		if err != nil {
			f.t.Fatal(err)
		}
		for _, record := range records {
			if record.Kind == lock.TransactionRecord && record.Waiting && record.Owner.Session == creator.Token().ID() {
				return true
			}
		}
		return false
	})
}

// eventually polls cond until it holds, failing after the liveness deadline.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(liveness)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for", what)
		}
		runtime.Gosched()
	}
}

func receive[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(liveness):
		t.Fatal("timed out waiting for", what)
		panic("unreachable")
	}
}

// signalled waits for a closed channel within the liveness deadline.
func signalled(t *testing.T, what string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(liveness):
		t.Fatal("timed out waiting for", what)
	}
}

type outcome struct {
	rows [][]any
	err  error
}

func async(ctx context.Context, s *session.Session, sql string) <-chan outcome {
	done := make(chan outcome, 1)
	go func() {
		rows, err := runContext(ctx, s, sql)
		done <- outcome{rows, err}
	}()
	return done
}

// One decoder uses a slot at a time; commits racing a decoder are neither
// lost nor returned twice.
func TestLogicalStreamBusySlot(t *testing.T) {
	f := newFixture(t)
	a, b, c := f.session(), f.session(), f.session()
	f.must(a, "CREATE TABLE t (id int)")
	f.slot(a, "busy")
	f.must(a, "INSERT INTO t VALUES (1)")
	pid := f.value(a, "SELECT pg_backend_pid()::text")
	entered, release := f.probe.arm()
	done := async(context.Background(), a, "SELECT data FROM pg_logical_slot_get_changes('busy', NULL, NULL)")
	signalled(t, "an active decoder", entered)
	if got := f.value(b, "SELECT active_pid::text FROM pg_replication_slots WHERE slot_name = 'busy'"); got != pid {
		t.Fatal("active decoder", got, pid)
	}
	busy := `replication slot "busy" is active for PID ` + pid.(string)
	f.fails(b, "SELECT * FROM pg_logical_slot_peek_changes('busy', NULL, NULL)", "55006", busy)
	f.fails(b, "SELECT pg_drop_replication_slot('busy')", "55006", busy)
	f.fails(b, "SELECT pg_replication_slot_advance('busy', 'FFFFFFFF/0')", "55006", busy)
	f.must(b, "SELECT pg_copy_logical_replication_slot('busy', 'busy_copy')")
	f.must(c, "INSERT INTO t VALUES (2)") // Commits while the decoder runs.
	release()
	got := receive(t, "the decoder", done)
	if got.err != nil || len(got.rows) != 3 {
		t.Fatal("decoder result", got)
	}
	f.expect(f.changes(b, "busy"), tx(row("2"))...)
	f.expect(f.changes(b, "busy_copy"), concat(tx(row("1")), tx(row("2")))...)
	if got := f.value(b, "SELECT active FROM pg_replication_slots WHERE slot_name = 'busy'"); got != "f" {
		t.Fatal("decoder left the slot active", got)
	}
}

// Plugin failure and cancellation release the slot without consuming.
func TestLogicalStreamDecoderFailure(t *testing.T) {
	f := newFixture(t)
	s, peer := f.session(), f.session()
	f.must(s, "CREATE TABLE t (id int)")
	f.slot(s, "fragile")
	f.must(s, "INSERT INTO t VALUES (1)")
	want := tx(row("1"))
	for _, callback := range []string{"begin", "change", "commit"} {
		f.fails(s, "SELECT * FROM pg_logical_slot_get_changes('fragile', NULL, NULL, 'fail', '"+callback+"')", "XX000", "recorder: "+callback+" failure")
		f.expect(f.peek(peer, "fragile"), want...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	entered, release := f.probe.arm()
	done := async(ctx, s, "SELECT data FROM pg_logical_slot_get_changes('fragile', NULL, NULL)")
	signalled(t, "an active decoder", entered)
	cancel()
	release()
	if got := receive(t, "the cancelled decoder", done); !errors.Is(got.err, context.Canceled) {
		t.Fatal("cancelled decoding", got)
	}
	if got := f.value(peer, "SELECT active FROM pg_replication_slots WHERE slot_name = 'fragile'"); got != "f" {
		t.Fatal("cancelled decoder left the slot active", got)
	}
	f.expect(f.changes(peer, "fragile"), want...)
	starts, stops := f.probe.counts()
	if starts != stops {
		t.Fatal("plugin contexts left open", starts, stops)
	}
}

// Creation waits, cancellably, for transactions holding an XID; they are not
// decoded, while later transactions are.
func TestLogicalStreamCreationBoundary(t *testing.T) {
	f := newFixture(t)
	w, c, x, observer := f.session(), f.session(), f.session(), f.session()
	f.must(w, "CREATE TABLE t (id int)")
	f.must(w, "BEGIN")
	f.must(w, "INSERT INTO t VALUES (1)")
	f.must(x, "BEGIN") // No XID yet: not waited for.
	creating := func(name string) {
		t.Helper()
		eventually(t, "slot "+name+" waiting for its consistent point", func() bool {
			return f.value(observer, "SELECT count(*) FROM pg_replication_slots WHERE slot_name = '"+name+"' AND confirmed_flush_lsn IS NULL AND active") == "1"
		})
		f.waitCreationBoundary(c)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := async(ctx, c, "SELECT pg_create_logical_replication_slot('boundary', 'recorder')")
	creating("boundary")
	cancel()
	if got := receive(t, "the cancelled creation", done); !errors.Is(got.err, context.Canceled) {
		t.Fatal("cancelled creation", got)
	}
	if got := f.value(observer, "SELECT count(*) FROM pg_replication_slots"); got != "0" {
		t.Fatal("cancelled creation left a slot", got)
	}

	done = async(context.Background(), c, "SELECT pg_create_logical_replication_slot('boundary', 'recorder')")
	creating("boundary")
	f.must(x, "INSERT INTO t VALUES (2)")
	f.must(x, "COMMIT") // Before the consistent point.
	f.must(w, "INSERT INTO t VALUES (3)")
	f.must(w, "COMMIT")
	if got := receive(t, "the creation", done); got.err != nil {
		t.Fatal("creation", got.err)
	}
	f.must(x, "INSERT INTO t VALUES (4)")
	f.expect(f.changes(observer, "boundary"), tx(row("4"))...)

	// A prepared transaction holds its XID until it finishes.
	f.must(w, "BEGIN")
	f.must(w, "INSERT INTO t VALUES (5)")
	f.must(w, "PREPARE TRANSACTION 'held'")
	done = async(context.Background(), c, "SELECT pg_create_logical_replication_slot('after_prepare', 'recorder')")
	creating("after_prepare")
	f.must(w, "COMMIT PREPARED 'held'")
	if got := receive(t, "the creation after PREPARE", done); got.err != nil {
		t.Fatal("creation after prepare", got.err)
	}
	f.must(x, "INSERT INTO t VALUES (6)")
	f.expect(f.changes(observer, "after_prepare"), tx(row("6"))...)
	f.expect(f.changes(observer, "boundary"), concat(tx(row("5")), tx(row("6")))...)
}

// Live authority governs every execution of a retained statement, and a
// retained statement never keeps a dropped slot's state.
func TestLogicalStreamRetainedStatements(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "CREATE TABLE t (id int)")
	f.must(s, "CREATE ROLE decoder")
	f.slot(s, "kept")
	f.must(s, "PREPARE peek AS SELECT data FROM pg_logical_slot_peek_changes('kept', NULL, NULL)")
	f.must(s, "PREPARE take AS SELECT data FROM pg_logical_slot_get_changes('kept', NULL, NULL)")
	f.must(s, "INSERT INTO t VALUES (1)")
	f.must(s, "SET ROLE decoder")
	f.fails(s, "EXECUTE peek", "42501", "permission denied to use replication slots")
	f.must(s, "RESET ROLE")
	f.must(s, "ALTER ROLE decoder REPLICATION")
	f.must(s, "SET ROLE decoder")
	if got := f.must(s, "EXECUTE peek"); len(got) != 3 {
		t.Fatal("authorized execution", got)
	}
	f.must(s, "RESET ROLE")
	f.must(s, "SELECT pg_drop_replication_slot('kept')")
	f.fails(s, "EXECUTE take", "42704", `replication slot "kept" does not exist`)
	f.slot(s, "kept")
	f.must(s, "INSERT INTO t VALUES (2)")
	if got := f.must(s, "EXECUTE take"); len(got) != 3 || got[1][0] != row("2") {
		t.Fatal("recreated slot", got)
	}
}

// A transaction prepared while a two-phase slot waits for its consistent point
// is sent whole, with its prepare, at COMMIT PREPARED.
func TestLogicalStreamTwoPhaseCreationBoundary(t *testing.T) {
	f := newFixture(t)
	w, c, x, observer := f.session(), f.session(), f.session(), f.session()
	f.must(w, "CREATE TABLE t (id int)")
	f.must(w, "BEGIN")
	f.must(w, "INSERT INTO t VALUES (1)")
	f.must(x, "BEGIN")
	done := async(context.Background(), c, "SELECT pg_create_logical_replication_slot('tp', 'recorder', false, true)")
	eventually(t, "the two-phase slot waiting", func() bool {
		return f.value(observer, "SELECT count(*) FROM pg_replication_slots WHERE slot_name = 'tp' AND confirmed_flush_lsn IS NULL") == "1"
	})
	f.waitCreationBoundary(c)
	f.must(x, "INSERT INTO t VALUES (2)")
	f.must(x, "PREPARE TRANSACTION 'px'")
	f.must(w, "COMMIT")
	if got := receive(t, "the creation", done); got.err != nil {
		t.Fatal("creation", got.err)
	}
	f.expect(f.changes(observer, "tp"))
	f.must(observer, "COMMIT PREPARED 'px'")
	f.expect(f.changes(observer, "tp"), "BEGIN PREPARE px", row("2"), "PREPARE px", "COMMIT PREPARED px")
}
