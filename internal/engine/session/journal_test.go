package session

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type probeEnum struct{ clones *int }

func (probeEnum) EnumSlot() {}
func (p probeEnum) Clone() EnumSlot {
	*p.clones++
	return probeEnum{clones: p.clones}
}

type probeFence FenceKind

func (f probeFence) Kind() FenceKind { return FenceKind(f) }

func TestJournalCloneOfNilIsEmpty(t *testing.T) {
	var c *Journal
	n := c.Clone()
	if n == nil || n.Originals == nil || n.Descriptors == nil || n.Names == nil || n.Generators == nil || n.Storage == nil {
		t.Fatalf("nil journal clone = %+v, want an empty journal", n)
	}
	if n.Enum != nil || n.Domain != nil || n.Indexes != nil || len(n.Fences) != 0 {
		t.Fatalf("nil journal clone has slots or fences: %+v", n)
	}
}

func TestJournalCloneKeepsNilSlotsNil(t *testing.T) {
	n := NewJournal().Clone()
	if n.Enum != nil || n.Domain != nil || n.Indexes != nil {
		t.Fatalf("clone filled nil slots: enum %v, domain %v, indexes %v", n.Enum, n.Domain, n.Indexes)
	}
}

func TestJournalCloneCopiesMapsAndSlotsAndSharesFences(t *testing.T) {
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1}
	clones := 0
	c := NewJournal()
	c.Descriptors[address] = nil
	c.Enum = probeEnum{clones: &clones}
	c.AddFence(probeFence(2))
	c.AddFence(probeFence(1))
	c.DefaultACLs = map[catalog.DefaultACLKey]*catalog.DefaultACLRecord{{}: nil}

	n := c.Clone()
	if clones != 1 || n.Enum == nil {
		t.Fatalf("enum slot cloned %d times, clone slot %v", clones, n.Enum)
	}
	delete(n.Descriptors, address)
	if _, ok := c.Descriptors[address]; !ok {
		t.Fatal("clone shares the descriptor map with its source")
	}
	if len(n.DefaultACLs) != 1 {
		t.Fatalf("clone default ACLs = %v", n.DefaultACLs)
	}
	if len(n.Fences) != 2 || n.Fences[0] != c.Fences[0] || n.Fences[1] != c.Fences[1] {
		t.Fatalf("clone fences = %v, want %v in recording order", n.Fences, c.Fences)
	}
	n.AddFence(probeFence(3))
	if len(c.Fences) != 2 {
		t.Fatalf("fence added to the clone reached the source: %v", c.Fences)
	}
}

func TestJournalDescriptorEditHistory(t *testing.T) {
	a := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 101}
	b := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 102}
	c := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 103}
	base := catalog.NewState(1)
	first, second := &catalog.Namespace{}, &catalog.Namespace{}
	journal := NewJournal()
	empty := journal.DescriptorEditMark()
	if empty != (DescriptorEditMark{}) || journal.DescriptorEditSerial() != 0 {
		t.Fatal("new journal has descriptor history")
	}
	// Compare each actual visit in place: no materialized replay slice.
	wantHistory := func(j *Journal, mark DescriptorEditMark, want ...ids.ObjectAddress) {
		t.Helper()
		n := 0
		trusted := j.DescriptorEditsSince(mark, func(address ids.ObjectAddress) bool {
			if n >= len(want) || address != want[n] {
				t.Fatalf("history visit %d = %v, want %v", n, address, want)
			}
			n++
			return true
		})
		if !trusted || n != len(want) {
			t.Fatalf("history trusted=%v, visits=%d, want %d", trusted, n, len(want))
		}
	}
	journal.RecordDescriptor(base, a, first)
	if journal.DescriptorEditSerial() != 1 {
		t.Fatal("first write did not advance serial once")
	}
	one := journal.DescriptorEditMark()
	wantHistory(journal, empty, a)
	journal.RecordDescriptor(base, b, second)
	if journal.DescriptorEditSerial() != 2 {
		t.Fatal("second write did not advance serial once")
	}
	journal.RecordDescriptor(base, a, nil)
	if journal.DescriptorEditSerial() != 3 || journal.Descriptors[a] != nil {
		t.Fatal("duplicate tombstone write did not advance history")
	}
	if _, present := journal.Descriptors[a]; !present {
		t.Fatal("tombstone lost its descriptor key")
	}
	wantHistory(journal, empty, a, b, a)
	wantHistory(journal, one, a, b)
	wantHistory(journal, journal.DescriptorEditMark())

	parentMark := journal.DescriptorEditMark()
	child, divergent := journal.Clone(), journal.Clone()
	if child.descriptorEdits != journal.descriptorEdits || child.DescriptorEditMark() != parentMark {
		t.Fatal("clone rebuilt rather than shared immutable history")
	}
	child.RecordDescriptor(base, c, first)
	divergent.RecordDescriptor(base, b, nil)
	if child.DescriptorEditSerial() != 4 || divergent.DescriptorEditSerial() != 4 ||
		child.DescriptorEditMark() == divergent.DescriptorEditMark() {
		t.Fatal("divergent equal-serial writes did not retain distinct ancestry")
	}
	for _, pair := range [][2]*Journal{{journal, child}, {child, divergent}, {divergent, child}} {
		if pair[0].DescriptorEditsSince(pair[1].DescriptorEditMark(), func(ids.ObjectAddress) bool {
			t.Fatal("unreachable mark yielded authenticated edits")
			return false
		}) {
			t.Fatal("unreachable child/divergent mark authenticated by serial")
		}
	}
	if journal.DescriptorEditSerial() != 3 || journal.Descriptors[b] != second {
		t.Fatal("child write mutated parent history or descriptor")
	}
	if _, present := journal.Descriptors[c]; present {
		t.Fatal("child descriptor escaped into parent")
	}
	wantHistory(journal, one, a, b)
	wantHistory(child, parentMark, c)
	wantHistory(divergent, parentMark, b)

	// Exercise the actual savepoint journal capture/restore seam without a
	// composed engine import (which would cycle through session).
	owner := &TransactionOwner{Session: &Session{Settings: &SettingsStore{}}, Catalog: journal}
	mark := owner.CaptureSavepoint(SavepointMark{Name: "descriptor_history"})
	owner.Catalog = child
	owner.Session.Settings.Mu.Lock()
	owner.Session.Mu.Lock()
	owner.RollbackToLocked(mark, mark.Settings)
	owner.Session.Mu.Unlock()
	owner.Session.Settings.Mu.Unlock()
	if owner.Catalog != journal || owner.Catalog.DescriptorEditMark() != parentMark {
		t.Fatal("savepoint rollback failed to restore older journal history")
	}
	wantHistory(owner.Catalog, one, a, b)
	reappend := owner.Catalog.Clone()
	reappend.RecordDescriptor(base, c, second)
	if reappend.DescriptorEditsSince(child.DescriptorEditMark(), func(ids.ObjectAddress) bool { return true }) {
		t.Fatal("rollback/reappend authenticated discarded child with equal serial")
	}
	wantHistory(child, parentMark, c)
	wantHistory(journal, parentMark)

	for _, mutation := range []struct {
		name string
		edit func(*Journal)
	}{
		{"new_key", func(j *Journal) { j.Descriptors[c] = first }},
		{"delete", func(j *Journal) { delete(j.Descriptors, a) }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			forged := journal.Clone()
			mutation.edit(forged)
			if forged.DescriptorEditsSince(parentMark, func(ids.ObjectAddress) bool { return true }) {
				t.Fatal("manual descriptor key mutation retained history trust")
			}
			wantHistory(journal, empty, a, b, a)
		})
	}
	// Direct same-key replacement without RecordDescriptor is deliberately
	// not treated as safe: key counts cannot detect it. Production writers
	// must record edits; this test makes no authority claim for that residual.
}
