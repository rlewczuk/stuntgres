package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// These tests exercise physical identities and authority directly. SQL behavior
// and PostgreSQL expectations belong to the public dual-mode definitions.
func inheritanceDMLScope(t *testing.T, e *Engine, p *catalog.Relation, ctx context.Context) (*StatementScope, func()) {
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
	s.lockedRef = e.catalog.Domain.Ref(e.CatalogRead().Epoch(), p.Address())
	s.lockedDescriptor = p
	s.lockedStorage = e.storage.Relations[p.Address()]
	if err = e.locks.Acquire(ctx, s.ticket, lock.Resource{ObjectRef: s.lockedRef}, lock.RowExclusive); err != nil {
		s.Finish(err)
		done()
		t.Fatal(err)
	}
	return s, func() { s.Finish(nil); done() }
}

func TestInheritanceDMLAdmission(t *testing.T) {
	e := New()
	p := inheritanceLifecycleTable(e, 22000, "p")
	l := inheritanceLifecycleTable(e, 22001, "l", p.Address())
	r := inheritanceLifecycleTable(e, 22002, "r", p.Address())
	d := inheritanceLifecycleTable(e, 22003, "d", l.Address(), r.Address())
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
	lockState := e.locks.Inspect()
	for _, ref := range refs {
		if seen[ref] {
			t.Fatal("duplicate physical identity", ref)
		}
		seen[ref] = true
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		if entry == nil || entry.Holders[s.ticket].Mode&lock.RowExclusive == 0 {
			t.Fatal("missing physical RX grant", ref)
		}
	}
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
	ref := func(d *catalog.Relation) ids.ObjectRef {
		return e.catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
	}
	old := rowVersion{Row: 7, Version: 11, Base: 5, Born: 3, Values: []sqltype.Value{inheritanceQueryInt(e, 99), inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 1)}}
	physical := queryPhysicalRow{leaf: queryLeaf{ref: ref(c), relation: c, storage: &relationStorage{}}, version: old, hint: 6, segment: &inheritanceQuerySegment{layout: layout, scratch: make([]sqltype.Value, 2)}}
	proposed := []sqltype.Value{inheritanceQueryInt(e, 8), inheritanceQueryInt(e, 2)}
	scattered := scatterMutationRow(physical, old, proposed, []int{0})
	want := []sqltype.Value{old.Values[0], old.Values[1], proposed[0]}
	if !reflect.DeepEqual(scattered, want) || !reflect.DeepEqual(old.Values, []sqltype.Value{inheritanceQueryInt(e, 99), inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 1)}) {
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
	own.version.Born = 0
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
	c := inheritanceLifecycleTable(e, 22001, "c", p.Address())
	outsider := inheritanceLifecycleTable(e, 22002, "outsider")
	inheritanceLifecycleInstall(e, p, c, outsider)
	s, done := inheritanceDMLScope(t, e, p, context.Background())
	defer done()
	if err := s.acquireInheritedMutationTargets(&parser.Delete{}); err != nil {
		t.Fatal(err)
	}
	child := s.inheritedTargets[0]
	if s.mutationStorage(child.ref) != e.storage.Relations[c.Address()] || s.mutationStorage(s.lockedRef) != e.storage.Relations[p.Address()] {
		t.Fatal("manifest lost stores")
	}
	foreign := child.ref
	foreign.Address = outsider.Address()
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
	next.ObjectRevision++
	next.ObjectName = "renamed"
	e.catalog.state = catalog.CloneState(e.catalog.state)
	e.catalog.state.SetDescriptor(c.Address(), &next)
	if err := s.acquireInheritedMutationTargets(&parser.Update{}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed descendant silently rebound", err)
	}
	if s.inheritedTargets[0].ref != before.ref || s.inheritedTargets[0].relation != before.relation || s.inheritedTargets[0].storage != before.storage {
		t.Fatal("failed admission retargeted frozen manifest")
	}
}
