package logical

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/core"
)

// The public rollback-only prepared matrix specifies release-oracle output.
// Here the actual prepared descriptor, retained event backing and synthetic
// restart frontier prove ownership that PostgreSQL's checkpointed restart_lsn
// cannot expose synchronously through the public oracle.
func TestLogicalStreamRollbackOnlyPreparedRetention(t *testing.T) {
	for _, twoPhase := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("two_phase=%v/commit=%v", twoPhase, commit), func(t *testing.T) {
				f, owner, reader, writer := eventFixture(t)
				f.must(reader, "SELECT pg_drop_replication_slot('events')")
				f.must(reader, fmt.Sprintf("SELECT pg_create_logical_replication_slot('events','witness',false,%v)", twoPhase))
				f.must(writer, "BEGIN")
				f.must(writer, "SAVEPOINT discarded")
				f.must(writer, "INSERT INTO t VALUES(1,'discarded')")
				if rows := f.peek(reader, "events", streamOptions...); len(rows) != 3 || len(owner.changes) != 1 {
					t.Fatal("real active child not captured", rows, owner.changes)
				}
				event := owner.changes[0]
				anchor := event.txn.Transaction
				if anchor == nil || anchor.FirstLSN == 0 || event.change.TopXID == event.change.SubXID || event.change.SubXID == 0 {
					t.Fatal("missing genuine child/anchor identity", event)
				}
				anchorValue := *anchor
				values := slices.Clone(event.change.New)
				backing := &event.change.New[0]
				relation := event.change.Relation
				columns := slices.Clone(relation.Columns)
				f.must(writer, "ROLLBACK TO discarded")
				prepared := false
				defer func() {
					if prepared {
						if _, err := run(writer, "ROLLBACK PREPARED 'rollback_only_owner'"); err != nil {
							t.Error(err)
						}
					}
				}()
				f.must(writer, "PREPARE TRANSACTION 'rollback_only_owner'")
				prepared = true
				f.e.Mu.Lock()
				entry := core.EnginePreparedRef(f.e).Lookup("rollback_only_owner")
				f.e.Mu.Unlock()
				if entry == nil || len(entry.LogicalChanges) != 0 || entry.LogicalTxn == nil {
					t.Fatal("projection-empty PREPARE lost real journal descriptor", entry)
				}
				descriptor := entry.LogicalTxn
				savedDescriptor := *descriptor
				if descriptor.XID != anchor.XID || descriptor.FirstLSN != anchor.FirstLSN || descriptor.PrepareLSN <= anchor.FirstLSN || descriptor.GID != "rollback_only_owner" || len(descriptor.Changes) != 0 {
					t.Fatal("prepared descriptor lost anchor or manufactured effects", descriptor, anchor)
				}
				f.changes(reader, "events", streamOptions...)
				if pinned := f.value(reader, "SELECT restart_lsn < confirmed_flush_lsn FROM pg_replication_slots WHERE slot_name='events'"); pinned != "t" {
					t.Fatal("prepared history not retained beyond consumed frontier", pinned)
				}
				if rows, err := run(reader, "SELECT id FROM t"); err != nil || len(rows) != 0 {
					t.Fatal("rolled-back effect visible while prepared", rows, err)
				}
				finish := "ROLLBACK"
				if commit {
					finish = "COMMIT"
				}
				f.must(writer, finish+" PREPARED 'rollback_only_owner'")
				prepared = false
				f.changes(reader, "events", streamOptions...)
				if released := f.value(reader, "SELECT restart_lsn = confirmed_flush_lsn FROM pg_replication_slots WHERE slot_name='events'"); released != "t" {
					t.Fatal("finished rollback-only transaction retained restart anchor", released)
				}
				f.must(writer, "INSERT INTO t VALUES(2,'later')")
				f.changes(reader, "events", streamOptions...)
				if advanced := f.value(reader, "SELECT restart_lsn = confirmed_flush_lsn FROM pg_replication_slots WHERE slot_name='events'"); advanced != "t" {
					t.Fatal("finished anchor pinned subsequent history", advanced)
				}
				f.e.Mu.Lock()
				remaining := core.EnginePreparedRef(f.e).Lookup("rollback_only_owner")
				f.e.Mu.Unlock()
				if remaining != nil {
					t.Fatal("finished descriptor still registered", remaining)
				}
				f.must(reader, "SELECT pg_drop_replication_slot('events')")
				if !reflect.DeepEqual(*descriptor, savedDescriptor) || !reflect.DeepEqual(*anchor, anchorValue) || event.change.Relation != relation || &event.change.New[0] != backing || !reflect.DeepEqual(event.change.New, values) || !reflect.DeepEqual(relation.Columns, columns) {
					t.Fatal("finish/trim/drop mutated retained event or descriptor")
				}
			})
		}
	}
}
