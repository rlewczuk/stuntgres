package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// These tests exercise physical identities and authority directly. SQL behavior
// and PostgreSQL expectations belong to the public dual-mode definitions.
func inheritanceDMLScope(t *testing.T, e *core.Engine, p *catalog.Relation, ctx context.Context) (*core.StatementScope, func()) {
	t.Helper()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.StartStatement(op)
	if err != nil {
		done()
		t.Fatal(err)
	}
	if err = s.Gate(&parser.Update{}); err != nil {
		s.Finish(err)
		done()
		t.Fatal(err)
	}
	s.LockedRef = e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), p.Address())
	s.LockedDescriptor = p
	s.LockedStorage = e.Storage.Relations[p.Address()]
	if err = e.Locks.Acquire(ctx, s.LockTicket, lock.Resource{ObjectRef: s.LockedRef}, lock.RowExclusive); err != nil {
		s.Finish(err)
		done()
		t.Fatal(err)
	}
	return s, func() { s.Finish(nil); done() }
}

func TestInheritanceDMLAdmission(t *testing.T) {
	e := engine.New()
	p := inheritanceLifecycleTable(e, 22000, "p")
	l := inheritanceLifecycleTable(e, 22001, "l", p.Address())
	r := inheritanceLifecycleTable(e, 22002, "r", p.Address())
	d := inheritanceLifecycleTable(e, 22003, "d", l.Address(), r.Address())
	inheritanceLifecycleInstall(e, p, l, r, d)
	s, done := inheritanceDMLScope(t, e, p, context.Background())
	defer done()
	if err := core.ScopeAcquireInheritedMutationTargets(s, &parser.Update{Only: true}); err != nil || len(s.InheritedTargets) != 0 {
		t.Fatal("ONLY acquired descendants", err)
	}
	if err := core.ScopeAcquireInheritedMutationTargets(s, &parser.Update{}); err != nil {
		t.Fatal(err)
	}
	refs := s.Command.TargetRefs(s.LockedRef)
	if len(refs) != 4 {
		t.Fatal("diamond was not deduplicated", refs)
	}
	seen := map[ids.ObjectRef]bool{}
	lockState := e.Locks.Inspect()
	for _, ref := range refs {
		if seen[ref] {
			t.Fatal("duplicate physical identity", ref)
		}
		seen[ref] = true
		entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
		if entry == nil || entry.Holders[s.LockTicket].Mode&lock.RowExclusive == 0 {
			t.Fatal("missing physical RX grant", ref)
		}
	}
	if err := core.ScopeAcquireInheritedMutationTargets(s, &parser.Delete{}); err != nil || len(s.InheritedTargets) != 3 {
		t.Fatal("readmission duplicated targets", err)
	}
}

func TestInheritanceDMLLocator(t *testing.T) {
	e := engine.New()
	p := inheritanceQueryUnitColumns(e, 22000, "a", "b")
	c := inheritanceQueryUnitColumns(e, 22001, "extra", "b", "a")
	layout, err := catalog.InheritanceQueryLayout(p, c)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(d *catalog.Relation) ids.ObjectRef {
		return e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), d.Address())
	}
	old := storage.RowVersion{Row: 7, Version: 11, Base: 5, Born: 3, Values: []sqltype.Value{inheritanceQueryInt(e, 99), inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 1)}}
	physical := exec.QueryPhysicalRow{Leaf: exec.Leaf{Ref: ref(c), Relation: c, Storage: &storage.Relation{}}, Version: old, Hint: 6, Segment: &exec.InheritanceQuerySegment{Layout: layout, Scratch: make([]sqltype.Value, 2)}}
	proposed := []sqltype.Value{inheritanceQueryInt(e, 8), inheritanceQueryInt(e, 2)}
	scattered := dml.ScatterMutationRow(physical, old, proposed, []int{0})
	want := []sqltype.Value{old.Values[0], old.Values[1], proposed[0]}
	if !reflect.DeepEqual(scattered, want) || !reflect.DeepEqual(old.Values, []sqltype.Value{inheritanceQueryInt(e, 99), inheritanceQueryInt(e, 2), inheritanceQueryInt(e, 1)}) {
		t.Fatal("scatter lost child-only values or mutated predecessor", scattered, old)
	}
	deletion := physical.Deletion()
	if deletion.Ref != ref(c) || deletion.Storage != physical.Leaf.Storage || deletion.Row != 7 || deletion.Version != 11 || deletion.Base != 5 || deletion.Ordinal != 6 || deletion.Own {
		t.Fatal("delete lost physical provenance", deletion)
	}
	if (dml.MutationRowIdentity{Ref: ref(p), Row: 7}) == (dml.MutationRowIdentity{Ref: ref(c), Row: 7}) {
		t.Fatal("equal RowID aliased relations")
	}
	own := physical
	own.Version.Born = 0
	if !own.Deletion().Own {
		t.Fatal("prior-own provenance lost")
	}
	identity := exec.QueryPhysicalRow{}
	if got := dml.ScatterMutationRow(identity, old, proposed, []int{0}); &got[0] != &proposed[0] {
		t.Fatal("identity scatter copied proposal")
	}
}

func TestInheritanceDMLAuthority(t *testing.T) {
	e := engine.New()
	p := inheritanceLifecycleTable(e, 22000, "p")
	c := inheritanceLifecycleTable(e, 22001, "c", p.Address())
	outsider := inheritanceLifecycleTable(e, 22002, "outsider")
	inheritanceLifecycleInstall(e, p, c, outsider)
	s, done := inheritanceDMLScope(t, e, p, context.Background())
	defer done()
	if err := core.ScopeAcquireInheritedMutationTargets(s, &parser.Delete{}); err != nil {
		t.Fatal(err)
	}
	child := s.InheritedTargets[0]
	if core.ScopeMutationStorage(s, child.Ref) != e.Storage.Relations[c.Address()] || core.ScopeMutationStorage(s, s.LockedRef) != e.Storage.Relations[p.Address()] {
		t.Fatal("manifest lost stores")
	}
	foreign := child.Ref
	foreign.Address = outsider.Address()
	if core.ScopeMutationStorage(s, foreign) != nil {
		t.Fatal("unrelated live table gained mutation authority")
	}
	foreign = child.Ref
	foreign.Epoch++
	if core.ScopeMutationStorage(s, foreign) != nil {
		t.Fatal("wrong epoch gained mutation authority")
	}
	before := s.InheritedTargets[0]
	next := *c
	next.ObjectRevision++
	next.ObjectName = "renamed"
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	e.Catalog.State.SetDescriptor(c.Address(), &next)
	if err := core.ScopeAcquireInheritedMutationTargets(s, &parser.Update{}); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("changed descendant silently rebound", err)
	}
	if s.InheritedTargets[0].Ref != before.Ref || s.InheritedTargets[0].Relation != before.Relation || s.InheritedTargets[0].Storage != before.Storage {
		t.Fatal("failed admission retargeted frozen manifest")
	}
}
