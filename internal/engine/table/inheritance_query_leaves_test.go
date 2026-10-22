package table

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
)

// pendingFixture is p ← c1 ← c2 and an unrelated q, all in one namespace.
type pendingFixture struct {
	view          catalog.Read
	p, c1, c2, q  *catalog.Relation
	pRef, c1Ref   ids.ObjectRef
	c2Ref, qRef   ids.ObjectRef
	staleRef      ids.ObjectRef
	namespaceAddr ids.ObjectAddress
}

func newPendingFixture() pendingFixture {
	const epoch ids.CatalogEpoch = 3
	f := pendingFixture{namespaceAddr: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}}
	relation := func(oid ids.OID, parents ...*catalog.Relation) *catalog.Relation {
		d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: f.namespaceAddr, RelationKind: catalog.TableRelation}
		if len(parents) != 0 {
			d.Inheritance = &catalog.TableInheritance{}
			for i, parent := range parents {
				d.Inheritance.Parents = append(d.Inheritance.Parents, catalog.InheritanceParent{Relation: parent.Address(), Seqno: int32(i + 1)})
			}
		}
		return d
	}
	f.p = relation(100)
	f.c1 = relation(101, f.p)
	f.c2 = relation(102, f.c1)
	f.q = relation(103)
	state := catalog.NewState(epoch)
	state.SetDescriptor(f.namespaceAddr, &catalog.Namespace{ObjectAddress: f.namespaceAddr})
	for _, d := range []*catalog.Relation{f.p, f.c1, f.c2, f.q} {
		state.SetDescriptor(d.Address(), d)
	}
	f.view = catalog.NewRead(nil, state)
	ref := func(d *catalog.Relation) ids.ObjectRef { return ids.ObjectRef{Epoch: epoch, Address: d.Address()} }
	f.pRef, f.c1Ref, f.c2Ref, f.qRef = ref(f.p), ref(f.c1), ref(f.c2), ref(f.q)
	f.staleRef = ids.ObjectRef{Epoch: epoch - 1, Address: f.p.Address()}
	return f
}

type pendingLeaf struct {
	address ids.ObjectAddress
	mode    lock.Mode
}

func pendingOf(leaves []exec.Leaf) []pendingLeaf {
	var got []pendingLeaf
	for _, leaf := range leaves {
		got = append(got, pendingLeaf{leaf.Ref.Address, leaf.Mark.Mode})
	}
	return got
}

func TestInheritancePendingGrants(t *testing.T) {
	f := newPendingFixture()
	named := exec.Leaf{Ref: f.pRef, Relation: f.p}
	marked := exec.Leaf{Ref: f.pRef, Relation: f.p, Mark: plan.RowMark{Mode: lock.RowShare}}
	for _, tc := range []struct {
		name    string
		roots   []exec.Leaf
		grants  map[ids.ObjectRef]lock.Mode
		pending []pendingLeaf
		after   map[ids.ObjectRef]lock.Mode
	}{
		{
			name:    "named root skipped and descendants pending with a new map",
			roots:   []exec.Leaf{named},
			pending: []pendingLeaf{{f.c1.Address(), 0}, {f.c2.Address(), 0}},
			after:   map[ids.ObjectRef]lock.Mode{f.c1Ref: 0, f.c2Ref: 0},
		},
		{
			name:    "existing grant covers AccessShare",
			roots:   []exec.Leaf{named},
			grants:  map[ids.ObjectRef]lock.Mode{f.c1Ref: 0},
			pending: []pendingLeaf{{f.c2.Address(), 0}},
			after:   map[ids.ObjectRef]lock.Mode{f.c1Ref: 0, f.c2Ref: 0},
		},
		{
			name:    "AccessShare grant does not cover a row mark",
			roots:   []exec.Leaf{marked},
			grants:  map[ids.ObjectRef]lock.Mode{f.c1Ref: 0, f.c2Ref: lock.RowShare},
			pending: []pendingLeaf{{f.c1.Address(), lock.RowShare}},
			after:   map[ids.ObjectRef]lock.Mode{f.c1Ref: lock.RowShare, f.c2Ref: lock.RowShare},
		},
		{
			name:    "a later row mark upgrades a pending leaf",
			roots:   []exec.Leaf{named, marked},
			pending: []pendingLeaf{{f.c1.Address(), lock.RowShare}, {f.c2.Address(), lock.RowShare}},
			after:   map[ids.ObjectRef]lock.Mode{f.c1Ref: lock.RowShare, f.c2Ref: lock.RowShare},
		},
		{
			name:  "ONLY and childless roots need nothing",
			roots: []exec.Leaf{{Ref: f.pRef, Relation: f.p, Only: true}, {Ref: f.qRef, Relation: f.q}},
		},
		{
			name:    "an unnamed root is granted like a descendant",
			roots:   []exec.Leaf{{Ref: f.c2Ref}},
			pending: []pendingLeaf{{f.c2.Address(), 0}},
			after:   map[ids.ObjectRef]lock.Mode{f.c2Ref: 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pending, grants, err := InheritancePendingGrants(f.view, tc.roots, ids.SessionToken{}, tc.grants)
			if err != nil {
				t.Fatal(err)
			}
			if got := pendingOf(pending); !slices.Equal(got, tc.pending) {
				t.Fatalf("pending = %v, want %v", got, tc.pending)
			}
			if !maps.Equal(grants, tc.after) {
				t.Fatalf("grants = %v, want %v", grants, tc.after)
			}
			for _, leaf := range pending {
				if leaf.Relation == nil || leaf.Relation.Address() != leaf.Ref.Address || leaf.Storage != nil {
					t.Fatalf("pending leaf %v carries the wrong relation or a storage", leaf.Ref.Address)
				}
			}
		})
	}
	if _, _, err := InheritancePendingGrants(f.view, []exec.Leaf{{Ref: f.staleRef}}, ids.SessionToken{}, nil); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("stale root error = %v", err)
	}
}
