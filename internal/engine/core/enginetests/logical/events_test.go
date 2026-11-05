package logical

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	stream "stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/logical/testdecoding"
	"stuntgres/internal/engine/session"
)

type observedChange struct {
	change *stream.Change
	txn    stream.Txn
}
type eventOwner struct {
	changes []observedChange
	aborts  []stream.Txn
	e       *core.Engine
}
type observingPlugin struct {
	stream.Plugin
	owner *eventOwner
}

func (p *observingPlugin) observe(t *stream.Txn, c *stream.Change) {
	// Acquiring the real runtime lock would deadlock if callbacks ran under it.
	p.owner.e.Mu.Lock()
	p.owner.e.Mu.Unlock()
	p.owner.changes = append(p.owner.changes, observedChange{c, *t})
}
func (p *observingPlugin) Change(o *stream.Output, t *stream.Txn, c *stream.Change) error {
	p.observe(t, c)
	return p.Plugin.Change(o, t, c)
}
func (p *observingPlugin) Truncate(o *stream.Output, t *stream.Txn, c *stream.Change) error {
	return p.Plugin.(stream.TruncatePlugin).Truncate(o, t, c)
}
func (p *observingPlugin) StreamStart(o *stream.Output, t *stream.Txn) error {
	return p.Plugin.(stream.StreamingPlugin).StreamStart(o, t)
}
func (p *observingPlugin) StreamChange(o *stream.Output, t *stream.Txn, c *stream.Change) error {
	p.observe(t, c)
	return p.Plugin.(stream.StreamingPlugin).StreamChange(o, t, c)
}
func (p *observingPlugin) StreamTruncate(o *stream.Output, t *stream.Txn, c *stream.Change) error {
	p.observe(t, c)
	return p.Plugin.(stream.StreamingPlugin).StreamTruncate(o, t, c)
}
func (p *observingPlugin) StreamStop(o *stream.Output, t *stream.Txn) error {
	return p.Plugin.(stream.StreamingPlugin).StreamStop(o, t)
}
func (p *observingPlugin) StreamAbort(o *stream.Output, t *stream.Txn) error {
	p.owner.aborts = append(p.owner.aborts, *t)
	return p.Plugin.(stream.StreamingPlugin).StreamAbort(o, t)
}
func (p *observingPlugin) StreamCommit(o *stream.Output, t *stream.Txn) error {
	return p.Plugin.(stream.StreamingPlugin).StreamCommit(o, t)
}
func (p *observingPlugin) StreamPrepare(o *stream.Output, t *stream.Txn) error {
	return p.Plugin.(stream.StreamingPlugin).StreamPrepare(o, t)
}
func eventFixture(t *testing.T) (*fixture, *eventOwner, *session.Session, *session.Session) {
	owner := &eventOwner{}
	owner.e = engine.NewWithStartup(nil, "postgres", core.Startup{LogicalWAL: true, OutputPlugins: map[string]stream.Factory{"witness": func() stream.Plugin { return &observingPlugin{testdecoding.New(), owner} }}})
	f := &fixture{t: t, e: owner.e}
	reader := enginetest.StartSession(t, f.e, nil)
	writer := enginetest.StartSession(t, f.e, nil)
	f.must(reader, "SET output_plugin_libraries=witness")
	f.must(reader, "SET debug_logical_replication_streaming=immediate")
	f.must(reader, "CREATE TABLE t(id integer PRIMARY KEY,v text)")
	f.must(reader, "SELECT pg_create_logical_replication_slot('events','witness')")
	return f, owner, reader, writer
}

var streamOptions = []string{"include-xids", "0", "skip-empty-xacts", "1", "stream-changes", "1"}

func TestLogicalStreamEventOwnership(t *testing.T) {
	f, owner, r, w := eventFixture(t)
	f.must(w, "BEGIN")
	f.must(w, "INSERT INTO t VALUES(1,'old')")
	first := f.peek(r, "events", streamOptions...)
	if len(first) != 3 || len(owner.changes) != 1 {
		t.Fatal(first, owner.changes)
	}
	captured := owner.changes[0].change
	relation := captured.Relation
	backing := &captured.New[0]
	saved := slices.Clone(captured.New)
	columns := slices.Clone(relation.Columns)
	f.must(w, "COMMIT")
	f.must(w, "ALTER TABLE t ALTER COLUMN v TYPE integer USING length(v)")
	f.must(w, "ALTER TABLE t RENAME COLUMN v TO renamed")
	f.changes(r, "events", append(slices.Clone(streamOptions), "include-rewrites", "1")...)
	found := false
	for _, event := range owner.changes {
		if event.change.Rewrite {
			found = true
			if event.change.Relation.Columns[1].TypeName != "integer" {
				t.Fatal("rewrite did not own destination schema", event)
			}
		}
	}
	if !found {
		t.Fatal("actual rewrite stage not decoded")
	}
	f.must(r, "SELECT pg_drop_replication_slot('events')")
	if captured.Relation != relation || &captured.New[0] != backing || !reflect.DeepEqual(captured.New, saved) || !reflect.DeepEqual(relation.Columns, columns) {
		t.Fatal("old event backing changed")
	}
	text, err := captured.New[1].Text()
	if err != nil || text != "old" || relation.Columns[1].TypeName != "text" {
		t.Fatal("historic datum/type lost", text, err)
	}
}
func TestLogicalStreamSubtransactionHistory(t *testing.T) {
	for _, kind := range []string{"rollback", "nested", "release_parent", "repeated", "failed_statement", "exception", "session_end"} {
		t.Run(kind, func(t *testing.T) {
			f, owner, r, w := eventFixture(t)
			f.must(w, "BEGIN")
			f.must(w, "INSERT INTO t VALUES(1,'prefix')")
			if kind == "session_end" {
				f.peek(r, "events", streamOptions...)
				endSession(t, w)
				if got := f.peek(r, "events", streamOptions...); len(got) != 0 {
					t.Fatal("completed top abort replayed", got)
				}
				return
			}
			f.must(w, "SAVEPOINT outer_s")
			// Top-only inquiries leave the real child unassigned.
			f.must(w, "SELECT pg_current_xact_id()")
			w.Transactions.Mu.Lock()
			inquiryChild := w.Transactions.Savepoints[0].FullXID
			w.Transactions.Mu.Unlock()
			if inquiryChild != 0 {
				t.Fatal("inquiry assigned child")
			}
			if kind == "release_parent" || kind == "nested" {
				f.must(w, "SAVEPOINT inner_s")
			}
			if kind == "exception" {
				f.must(w, "DO $$BEGIN INSERT INTO t VALUES(2,'aborted'); RAISE EXCEPTION 'discard'; EXCEPTION WHEN raise_exception THEN PERFORM 1; END$$")
			} else {
				f.must(w, "INSERT INTO t VALUES(2,'aborted')")
			}
			w.Transactions.Mu.Lock()
			child := w.Transactions.Savepoints[len(w.Transactions.Savepoints)-1].FullXID
			w.Transactions.Mu.Unlock()
			if kind != "exception" && child == 0 {
				t.Fatal("actual modifying child has no identity")
			}
			if kind == "failed_statement" {
				if _, err := run(w, "INSERT INTO t VALUES(2,'attempted')"); err == nil {
					t.Fatal("duplicate insertion accepted")
				}
			}
			if kind == "release_parent" {
				f.must(w, "RELEASE inner_s")
			}
			f.must(w, "ROLLBACK TO outer_s")
			if kind == "repeated" {
				f.must(w, "INSERT INTO t VALUES(2,'again')")
				w.Transactions.Mu.Lock()
				next := w.Transactions.Savepoints[0].FullXID
				w.Transactions.Mu.Unlock()
				if next == 0 || next == child {
					t.Fatal("rollback reused child generation", child, next)
				}
				f.must(w, "ROLLBACK TO outer_s")
				f.e.Mu.Lock()
				status, ok, err := f.e.Visibility.TransactionStatusLocked(next)
				f.e.Mu.Unlock()
				if err != nil || !ok || status != "aborted" {
					t.Fatal("second child generation survived", status, ok, err)
				}
			}
			f.must(w, "INSERT INTO t VALUES(3,'survivor')")
			f.must(w, "COMMIT")
			f.peek(r, "events", streamOptions...)
			attempted := false
			aborted := false
			for _, event := range owner.changes {
				c := event.change
				v, _ := c.New[1].Text()
				attempted = attempted || v == "attempted"
				aborted = aborted || v == "aborted"
				if c.TopXID == 0 || c.SubXID == 0 || c.TopXID != event.txn.TopXID {
					t.Fatal("missing actual immutable event identity", c)
				}
				if (v == "aborted" || v == "attempted") && c.TopXID == c.SubXID {
					t.Fatal("child staging used parent identity", c)
				}
			}
			if !aborted || len(owner.aborts) == 0 || kind == "failed_statement" && !attempted {
				t.Fatal("real discarded staging evidence lost", owner.changes, owner.aborts)
			}
			for _, abort := range owner.aborts {
				if abort.XID == 0 || abort.XID == abort.TopXID {
					t.Fatal("synthetic/top child abort", abort)
				}
			}
			if child != 0 {
				f.e.Mu.Lock()
				status, ok, err := f.e.Visibility.TransactionStatusLocked(child)
				f.e.Mu.Unlock()
				if err != nil || !ok || status != "aborted" {
					t.Fatal("canonical child status", status, ok, err)
				}
			}
			got, err := runContext(context.Background(), w, "SELECT id FROM t ORDER BY id")
			if err != nil || !reflect.DeepEqual(got, [][]any{{"1"}, {"3"}}) {
				t.Fatal("aborted table effect published", got, err)
			}
		})
	}
}

func (p *observingPlugin) BeginPrepare(o *stream.Output, t *stream.Txn) error {
	return p.Plugin.(stream.TwoPhasePlugin).BeginPrepare(o, t)
}
func (p *observingPlugin) Prepare(o *stream.Output, t *stream.Txn) error {
	return p.Plugin.(stream.TwoPhasePlugin).Prepare(o, t)
}
func (p *observingPlugin) CommitPrepared(o *stream.Output, t *stream.Txn) error {
	return p.Plugin.(stream.TwoPhasePlugin).CommitPrepared(o, t)
}
func (p *observingPlugin) RollbackPrepared(o *stream.Output, t *stream.Txn) error {
	return p.Plugin.(stream.TwoPhasePlugin).RollbackPrepared(o, t)
}
func (p *observingPlugin) FilterByOrigin(origin uint32) bool {
	return p.Plugin.(stream.OriginFilter).FilterByOrigin(origin)
}

func TestLogicalStreamPreparedChildren(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			f, owner, r, w := eventFixture(t)
			f.must(r, "SELECT pg_drop_replication_slot('events')")
			f.must(r, "SELECT pg_create_logical_replication_slot('events','witness',false,true)")
			f.must(w, "BEGIN")
			f.must(w, "INSERT INTO t VALUES(1,'parent')")
			f.must(w, "SAVEPOINT child_s")
			f.must(w, "INSERT INTO t VALUES(2,'child')")
			w.Transactions.Mu.Lock()
			child := w.Transactions.Savepoints[0].FullXID
			w.Transactions.Mu.Unlock()
			prepared := false
			defer func() {
				if prepared {
					if _, err := run(w, "ROLLBACK PREPARED 'event_child'"); err != nil {
						t.Error(err)
					}
				}
			}()
			f.must(w, "PREPARE TRANSACTION 'event_child'")
			prepared = true
			f.e.Mu.Lock()
			record := f.e.Visibility.History[child]
			status, ok, err := f.e.Visibility.TransactionStatusLocked(child)
			f.e.Mu.Unlock()
			if child == 0 || err != nil || !ok || status != "in progress" || !record.Subcommitted {
				t.Fatal("prepared child lost actual lifetime", child, record, status, err)
			}
			rows := f.changes(r, "events", streamOptions...)
			if len(rows) != 7 || rows[len(rows)-1] != "preparing streamed transaction 'event_child'" {
				t.Fatal("streamed prepare lost child evidence", rows)
			}
			if len(owner.changes) != 2 || owner.changes[1].change.SubXID != uint32(child) {
				t.Fatal("prepared event child identity", owner.changes)
			}
			finish, want := "ROLLBACK", "aborted"
			if commit {
				finish, want = "COMMIT", "committed"
			}
			f.must(w, finish+" PREPARED 'event_child'")
			prepared = false
			f.e.Mu.Lock()
			status, ok, err = f.e.Visibility.TransactionStatusLocked(child)
			f.e.Mu.Unlock()
			if err != nil || !ok || status != want {
				t.Fatal("prepared child completion", status, ok, err)
			}
			f.expect(f.changes(r, "events", streamOptions...), finish+" PREPARED 'event_child'")
		})
	}
}

func TestLogicalStreamRewriteFailure(t *testing.T) {
	f, owner, r, w := eventFixture(t)
	f.must(w, "INSERT INTO t VALUES(1,'1'),(2,'bad')")
	f.changes(r, "events", "include-xids", "0", "skip-empty-xacts", "1")
	owner.changes = nil
	f.must(w, "BEGIN")
	f.must(w, "INSERT INTO t VALUES(3,'prefix')")
	f.must(w, "SAVEPOINT rewrite_s")
	f.fails(w, "ALTER TABLE t ALTER COLUMN v TYPE integer USING v::integer", "22P02", `invalid input syntax for type integer: "bad"`)
	f.must(w, "ROLLBACK TO rewrite_s")
	f.must(w, "INSERT INTO t VALUES(4,'survivor')")
	f.must(w, "COMMIT")
	f.peek(r, "events", append(slices.Clone(streamOptions), "include-rewrites", "1")...)
	var destination *stream.Change
	for _, event := range owner.changes {
		if event.change.Rewrite {
			destination = event.change
		}
	}
	if destination == nil || destination.Relation.Columns[1].TypeName != "integer" || destination.SubXID == destination.TopXID {
		t.Fatal("real pre-failure destination was not retained", destination)
	}
	value, err := destination.New[1].Integer()
	if err != nil || value != 1 || len(owner.aborts) == 0 {
		t.Fatal("failed rewrite evidence lost transformed row/abort", value, err, owner.aborts)
	}
	// The actual destination event survives in the journal, but its rejected
	// catalog stage must not join the ordinary successful-effect projection.
	f.expect(f.peek(r, "events", "include-xids", "0", "skip-empty-xacts", "1", "include-rewrites", "1"),
		"BEGIN", "table public.t: INSERT: id[integer]:3 v[text]:'prefix'",
		"table public.t: INSERT: id[integer]:4 v[text]:'survivor'", "COMMIT")
}
