package session

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestJournalCaptureObserve(t *testing.T) {
	schema := journalTestSchema(20001, "s")
	table := journalTestRelation(20010, "t", schema.Address())
	committed := journalTestState(schema, table)
	c := NewJournal()
	capture := JournalCapture{Journal: c, Committed: committed, Current: committed}
	if err := capture.Observe(table.Address(), table); err != nil || c.Originals[table.Address()] != table {
		t.Fatal("an observation did not record the committed original", err)
	}
	c.Originals[schema.Address()] = nil
	if err := capture.Observe(schema.Address(), schema); err != nil || c.Originals[schema.Address()] != nil {
		t.Fatal("an observation replaced the journal's own original", err)
	}
	stale := *table
	if err := capture.Observe(table.Address(), &stale); !isConflict(err) {
		t.Fatalf("stale observation: got %v, want a conflict", err)
	}

	database := schema.Address()
	if err := capture.ObserveDatabase(database, table, schema); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("changed authority: got %v, want a replaced catalog", err)
	}
	c.Originals[database] = table
	if err := capture.ObserveDatabase(database, schema, schema); !isConflict(err) {
		t.Fatalf("other original: got %v, want a conflict", err)
	}
	delete(c.Originals, database)
	if err := capture.ObserveDatabase(database, schema, schema); err != nil || c.Originals[database] != schema {
		t.Fatal("the database predecessor was not recorded as the original", err)
	}
}

func TestJournalMergeCandidate(t *testing.T) {
	owner := &catalog.Owner{}
	schema := journalTestSchema(20001, "s")
	table := journalTestRelation(20010, "t", schema.Address())
	committed := journalTestState(schema, table)
	c := NewJournal()
	private := *table
	private.ObjectName = "p"
	c.RecordDescriptor(committed, table.Address(), &private)
	observed := c.PrivateRead(owner, committed).State()
	candidate := catalog.NewCandidate(catalog.NewRead(owner, observed))
	next := private
	next.ObjectName = "q"
	if err := candidate.Replace(&next); err != nil {
		t.Fatal(err)
	}

	moved := catalog.CloneState(committed)
	concurrent := *table
	moved.SetDescriptor(table.Address(), &concurrent)
	if err := c.MergeCandidate(moved, observed, &candidate); !isConflict(err) || c.Descriptors[table.Address()] != &private {
		t.Fatal("a concurrent commit did not fail the merge before it recorded anything", err)
	}
	if err := c.MergeCandidate(committed, observed, &candidate); err != nil {
		t.Fatal(err)
	}
	if c.Descriptors[table.Address()] != &next || c.Originals[table.Address()] != table {
		t.Fatal("the merge did not record the change against the journal's original")
	}
}

func TestJournalCaptureFenceNamesWithoutIndexes(t *testing.T) {
	owner := &catalog.Owner{}
	schema := journalTestSchema(20001, "s")
	table := journalTestRelation(20010, "before", schema.Address())
	committed := journalTestState(schema, table)
	candidate := catalog.NewCandidate(catalog.NewRead(owner, committed))
	next := *table
	next.ObjectName = "after"
	if err := candidate.Replace(&next); err != nil {
		t.Fatal(err)
	}
	journal := NewJournal()
	capture := JournalCapture{Journal: journal, Candidate: &candidate, Committed: committed, Current: committed}
	if err := capture.FenceNames(nil); err != nil {
		t.Fatal("a name-only stage needs no index-parent iterator", err)
	}
	before, after := catalog.DescriptorNameKey(table), catalog.DescriptorNameKey(&next)
	if len(journal.Names) != 2 || journal.Names[before] != table.Address() || journal.Names[after] != (ids.ObjectAddress{}) {
		t.Fatal("omitting index parents lost committed source/absence fences", journal.Names)
	}
	changed := catalog.CloneState(committed)
	changed.BindName(after, table.Address())
	capture.Current = changed
	if err := capture.FenceNames(nil); !isConflict(err) {
		t.Fatal("omitting index parents bypassed actual name conflict", err)
	}
}
