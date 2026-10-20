package engine

import (
	"errors"
	"testing"
)

// catalogFencesOf returns the journal's fences of type F in recording order.
func catalogFencesOf[F catalogFence](c *catalogChanges) []F {
	var out []F
	for _, f := range c.fences {
		if typed, ok := f.(F); ok {
			out = append(out, typed)
		}
	}
	return out
}

type orderProbeFence struct {
	kind catalogFenceKind
	name string
	err  error
	seen *[]string
}

func (f orderProbeFence) fenceKind() catalogFenceKind { return f.kind }

func (f orderProbeFence) currentLocked(*Engine, *catalogChanges) error {
	*f.seen = append(*f.seen, f.name)
	return f.err
}

func TestCatalogFencesCheckKindsInCallOrder(t *testing.T) {
	var seen []string
	c := newCatalogChanges()
	for _, f := range []orderProbeFence{
		{kind: fenceNamespace, name: "namespace"},
		{kind: fenceStorageRead, name: "read-1"},
		{kind: fenceTruncateGraph, name: "truncate"},
		{kind: fenceIndexSet, name: "index"},
		{kind: fenceStorageRead, name: "read-2"},
	} {
		f.seen = &seen
		c.addFence(f)
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
	clone := cloneCatalogChanges(c)
	if len(clone.fences) != len(c.fences) || &clone.fences[0] == &c.fences[0] {
		t.Fatal("journal clone must copy the fence list in order into its own slice")
	}
}

func TestCatalogFencesStopAtFirstFailure(t *testing.T) {
	var seen []string
	first, second := errors.New("first"), errors.New("second")
	c := newCatalogChanges()
	c.addFence(orderProbeFence{kind: fenceIndexSet, name: "index", err: second, seen: &seen})
	c.addFence(orderProbeFence{kind: fenceStorageRead, name: "read", err: first, seen: &seen})
	if err := (&Engine{}).catalogFencesCurrentLocked(c, fenceStorageRead, fenceIndexSet); !errors.Is(err, first) {
		t.Fatalf("got %v, want the earlier kind's error", err)
	}
	if len(seen) != 1 || seen[0] != "read" {
		t.Fatalf("checked %v after the first failure", seen)
	}
}
