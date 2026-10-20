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
