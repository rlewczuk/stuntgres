package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// These tests exercise physical identities and authority directly. SQL behavior
// and PostgreSQL expectations belong to the public dual-mode definitions.
func inheritanceDMLScope(t *testing.T, e *Engine, p *relationDescriptor, ctx context.Context) (*StatementScope, func()) {
	t.Helper()
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartStatement(op)
	if err != nil {
		done()
		t.Fatal(err)
	}
	if err = s.gate(&parser.Update{}); err != nil {
		s.Finish(err)
		done()
		t.Fatal(err)
	}
	s.lockedRef = e.domain.Ref(e.CatalogRead().Epoch(), p.address)
	s.lockedDescriptor = p
	s.lockedStorage = e.storage[p.address]
	if err = e.locks.Acquire(ctx, s.ticket, lockResource{ObjectRef: s.lockedRef}, rowExclusive); err != nil {
		s.Finish(err)
		done()
		t.Fatal(err)
	}
	return s, func() { s.Finish(nil); done() }
}

func TestInheritanceDMLAdmission(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 22000, "p")
	l := inheritanceLifecycleTable(e, 22001, "l", p.address)
	r := inheritanceLifecycleTable(e, 22002, "r", p.address)
	d := inheritanceLifecycleTable(e, 22003, "d", l.address, r.address)
	inheritanceLifecycleInstall(e, p, l, r, d)
	s, done := inheritanceDMLScope(t, e, p, context.Background())
	defer done()
	if err := s.acquireInheritedMutationTargets(&parser.Update{Only: true}); err != nil || len(s.inheritedTargets) != 0 {
		t.Fatal("ONLY acquired descendants", err)
	}
	if err := s.acquireInheritedMutationTargets(&parser.Update{}); err != nil {
		t.Fatal(err)
	}
	refs := s.mutationTargetRefs(s.lockedRef)
	if len(refs) != 4 {
		t.Fatal("diamond was not deduplicated", refs)
	}
	seen := map[ids.ObjectRef]bool{}
	e.locks.mu.Lock()
	for _, ref := range refs {
		if seen[ref] {
			e.locks.mu.Unlock()
			t.Fatal("duplicate physical identity", ref)
		}
		seen[ref] = true
		entry := e.locks.entries[lockResource{ObjectRef: ref}]
		if entry == nil || entry.holders[s.ticket].Mode&rowExclusive == 0 {
			e.locks.mu.Unlock()
			t.Fatal("missing physical RX grant", ref)
		}
	}
	e.locks.mu.Unlock()
	if err := s.acquireInheritedMutationTargets(&parser.Delete{}); err != nil || len(s.inheritedTargets) != 3 {
		t.Fatal("readmission duplicated targets", err)
	}
}

func TestInheritanceDMLLocator(t *testing.T) {
	e := New()
	p := inheritanceQueryUnitColumns(e, 22000, "a", "b")
	c := inheritanceQueryUnitColumns(e, 22001, "extra", "b", "a")
	layout, err := inheritanceQueryLayout(p, c)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(d *relationDescriptor) ids.ObjectRef {
		return e.domain.Ref(e.CatalogRead().Epoch(), d.address)
	}
	old := rowVersion{row: 7, version: 11, base: 5, born: 3, values: []sqltype.Value{inheritanceQueryInt(e, 99), inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 1)}}
	physical := queryPhysicalRow{leaf: queryLeaf{ref: ref(c), relation: c, storage: &relationStorage{}}, version: old, hint: 6, segment: &inheritanceQuerySegment{layout: layout, scratch: make([]sqltype.Value, 2)}}
	proposed := []sqltype.Value{inheritanceQueryInt(e, 8), inheritanceQueryInt(e, 2)}
	scattered := scatterMutationRow(physical, old, proposed, []int{0})
	want := []sqltype.Value{old.values[0], old.values[1], proposed[0]}
	if !reflect.DeepEqual(scattered, want) || !reflect.DeepEqual(old.values, []sqltype.Value{inheritanceQueryInt(e, 99), inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 1)}) {
		t.Fatal("scatter lost child-only values or mutated predecessor", scattered, old)
	}
	deletion := physical.deletion()
	if deletion.ref != ref(c) || deletion.storage != physical.leaf.storage || deletion.row != 7 || deletion.version != 11 || deletion.base != 5 || deletion.ordinal != 6 || deletion.own {
		t.Fatal("delete lost physical provenance", deletion)
	}
	if (mutationRowIdentity{ref(p), 7}) == (mutationRowIdentity{ref(c), 7}) {
		t.Fatal("equal RowID aliased relations")
	}
	own := physical
	own.version.born = 0
	if !own.deletion().own {
		t.Fatal("prior-own provenance lost")
	}
	identity := queryPhysicalRow{}
	if got := scatterMutationRow(identity, old, proposed, []int{0}); &got[0] != &proposed[0] {
		t.Fatal("identity scatter copied proposal")
	}
}

func TestInheritanceDMLAuthority(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 22000, "p")
	c := inheritanceLifecycleTable(e, 22001, "c", p.address)
	outsider := inheritanceLifecycleTable(e, 22002, "outsider")
	inheritanceLifecycleInstall(e, p, c, outsider)
	s, done := inheritanceDMLScope(t, e, p, context.Background())
	defer done()
	if err := s.acquireInheritedMutationTargets(&parser.Delete{}); err != nil {
		t.Fatal(err)
	}
	child := s.inheritedTargets[0]
	if s.mutationStorage(child.ref) != e.storage[c.address] || s.mutationStorage(s.lockedRef) != e.storage[p.address] {
		t.Fatal("manifest lost stores")
	}
	foreign := child.ref
	foreign.Address = outsider.address
	if s.mutationStorage(foreign) != nil {
		t.Fatal("unrelated live table gained mutation authority")
	}
	foreign = child.ref
	foreign.Epoch++
	if s.mutationStorage(foreign) != nil {
		t.Fatal("wrong epoch gained mutation authority")
	}
	before := s.inheritedTargets[0]
	next := *c
	next.revision++
	next.name = "renamed"
	e.state = cloneCatalog(e.state)
	e.state.descriptors[c.address] = &next
	if err := s.acquireInheritedMutationTargets(&parser.Update{}); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("changed descendant silently rebound", err)
	}
	if s.inheritedTargets[0].ref != before.ref || s.inheritedTargets[0].relation != before.relation || s.inheritedTargets[0].storage != before.storage {
		t.Fatal("failed admission retargeted frozen manifest")
	}
}
