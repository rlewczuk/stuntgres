package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/session"
)

// catalogFencesOf returns the journal's fences of type F in recording order.
func catalogFencesOf[F catalogFence](c *session.Journal) []F {
	var out []F
	for _, f := range c.Fences {
		if typed, ok := f.(F); ok {
			out = append(out, typed)
		}
	}
	return out
}

type orderProbeFence struct {
	kind session.FenceKind
	name string
	err  error
	seen *[]string
}

func (f orderProbeFence) Kind() session.FenceKind { return f.kind }

func (f orderProbeFence) currentLocked(*Engine, *session.Journal) error {
	*f.seen = append(*f.seen, f.name)
	return f.err
}

func TestCatalogFencesCheckKindsInCallOrder(t *testing.T) {
	var seen []string
	c := session.NewJournal()
	for _, f := range []orderProbeFence{
		{kind: fenceNamespace, name: "namespace"},
		{kind: fenceStorageRead, name: "read-1"},
		{kind: fenceTruncateGraph, name: "truncate"},
		{kind: fenceIndexSet, name: "index"},
		{kind: fenceStorageRead, name: "read-2"},
	} {
		f.seen = &seen
		c.AddFence(f)
	}
	if err := (&Engine{}).catalogFencesCurrentLocked(c, fenceStorageRead, fenceIndexSet, fenceNamespace); err != nil {
		t.Fatal(err)
	}
	want := []string{"read-1", "read-2", "index", "namespace"}
	if len(seen) != len(want) {
		t.Fatalf("checked %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("checked %v, want %v", seen, want)
		}
	}
	clone := c.Clone()
	if len(clone.Fences) != len(c.Fences) || &clone.Fences[0] == &c.Fences[0] {
		t.Fatal("journal clone must copy the fence list in order into its own slice")
	}
}

func TestCatalogFencesStopAtFirstFailure(t *testing.T) {
	var seen []string
	first, second := errors.New("first"), errors.New("second")
	c := session.NewJournal()
	c.AddFence(orderProbeFence{kind: fenceIndexSet, name: "index", err: second, seen: &seen})
	c.AddFence(orderProbeFence{kind: fenceStorageRead, name: "read", err: first, seen: &seen})
	if err := (&Engine{}).catalogFencesCurrentLocked(c, fenceStorageRead, fenceIndexSet); !errors.Is(err, first) {
		t.Fatalf("got %v, want the earlier kind's error", err)
	}
	if len(seen) != 1 || seen[0] != "read" {
		t.Fatalf("checked %v after the first failure", seen)
	}
}
