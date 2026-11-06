package extension_test

import (
	"context"
	"slices"
	"sort"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

func TestDropClosureSeededRootDiagnosticOrder(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE EXTENSION cube",
		"CREATE EXTENSION earthdistance",
		"CREATE TABLE closure_earth(v earth)",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	var cube, earthdistance ids.ObjectAddress
	for address, descriptor := range base.Descriptors() {
		if ext, ok := descriptor.(*catalog.Extension); ok {
			switch ext.Name() {
			case "cube":
				cube = address
			case "earthdistance":
				earthdistance = address
			}
		}
	}
	if cube.ObjectID == 0 || earthdistance.ObjectID == 0 {
		t.Fatal("missing actual extension owners")
	}
	holder := base.MustRelation(enginetest.RoleTableRef(t, e, "closure_earth").Address)
	column := holder.Address()
	column.SubID = holder.Attributes[0]
	earth := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(holder.Columns[0].Usage.Type.ID())}
	roots := extension.RemovalRoots(base, []ids.ObjectAddress{cube})
	stage := e.BeginCatalogStage(base)
	defer e.DiscardCatalogStage(stage)
	for _, root := range roots {
		if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), root)); err != nil {
			t.Fatal(err)
		}
	}
	before := stage.RemovalCount()
	edges, err := view.DropClosure(core.StageDropProvider(context.Background(), stage, nil, nil, nil), roots)
	if err != nil {
		t.Fatal(err)
	}
	extensionSlot, columnSlot := -1, -1
	for i, edge := range edges {
		if edge.Address == earthdistance {
			if extensionSlot >= 0 || edge.Target != cube || edge.Automatic || !edge.Normal {
				t.Fatal("prerequisite edge changed", edge)
			}
			extensionSlot = i
		}
		if edge.Address == column {
			if columnSlot >= 0 || edge.Target != earth || edge.Automatic || !edge.Normal {
				t.Fatal("column parent/permission changed", edge)
			}
			columnSlot = i
		}
	}
	if extensionSlot < 0 || columnSlot <= extensionSlot || stage.RemovalCount() != before {
		t.Fatal("seeded provider preempted incumbent diagnostic order", extensionSlot, columnSlot)
	}
}

// REL_18_6 dependency.c visits extension dependents by descending object OID.
// Recipe slot order is not traversal order: a shared view must retain the later
// selected operator as its first parent, rather than its earlier input type.
func TestExtensionRemovalRootsTraversalOrder(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE EXTENSION seg",
		"CREATE EXTENSION unaccent",
		"CREATE TABLE seg_interval_closure_data(id integer, a text, b text)",
		"CREATE VIEW seg_interval_closure_view AS SELECT id, seg_overlap(a::seg,b::seg) AS via_function, a::seg && b::seg AS via_operator FROM seg_interval_closure_data",
	} {
		enginetest.Run(t, e, sql)
	}
	base := e.CatalogRead()
	seg := base.MustExtension(base.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "seg"}))
	unaccent := base.MustExtension(base.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	segMembers, unaccentMembers := slices.Clone(seg.Members), slices.Clone(unaccent.Members)
	data := enginetest.RoleTableRef(t, e, "seg_interval_closure_data").Address
	consumer := enginetest.RoleTableRef(t, e, "seg_interval_closure_view").Address
	t.Run("requested_prefix", func(t *testing.T) {
		// DROP OWNED can supply an ordinary root and an already requested
		// member alongside extensions. Preserve that deduplicated prefix even
		// though unaccent's later identities sort ahead of seg's globally.
		requests := []ids.ObjectAddress{data, seg.Address(), segMembers[3], unaccent.Address(), seg.Address(), segMembers[3]}
		savedRequests := slices.Clone(requests)
		want := []ids.ObjectAddress{data, seg.Address(), segMembers[3], unaccent.Address()}
		for _, members := range [][]ids.ObjectAddress{segMembers, unaccentMembers} {
			var block []ids.ObjectAddress
			for _, member := range members {
				if !slices.Contains(want, member) {
					block = append(block, member)
				}
			}
			// Recipe order is logical authority, not numeric allocation order.
			sort.Slice(block, func(i, j int) bool {
				a, b := block[i], block[j]
				if a.ObjectID != b.ObjectID {
					return a.ObjectID > b.ObjectID
				}
				if a.ClassID != b.ClassID {
					return a.ClassID < b.ClassID
				}
				return uint32(a.SubID) < uint32(b.SubID)
			})
			want = append(want, block...)
		}
		got := extension.RemovalRoots(base, requests)
		if !slices.Equal(got, want) {
			t.Fatal("requested roots or authentic member visitation changed", got, want)
		}
		if !slices.Equal(requests, savedRequests) {
			t.Fatal("expansion mutated requested roots", requests, savedRequests)
		}
		if !slices.Equal(seg.Members, segMembers) || !slices.Equal(unaccent.Members, unaccentMembers) {
			t.Fatal("expansion reordered a published manifest")
		}
		// Neither caller receives a writable view of its request or manifest.
		clear(got)
		if !slices.Equal(requests, savedRequests) || !slices.Equal(seg.Members, segMembers) || !slices.Equal(unaccent.Members, unaccentMembers) {
			t.Fatal("returned root storage aliases retained caller authority")
		}
	})
	t.Run("diagnostic_parent", func(t *testing.T) {
		var overlap ids.ObjectAddress
		for _, member := range segMembers {
			if member.ClassID == ids.OperatorClassID && base.Get(member).Name() == "&&" {
				overlap = member
			}
		}
		if overlap == (ids.ObjectAddress{}) {
			t.Fatal("missing genuine installed overlap operator")
		}
		roots := extension.RemovalRoots(base, []ids.ObjectAddress{seg.Address()})
		stage := e.BeginCatalogStage(base)
		defer e.DiscardCatalogStage(stage)
		for _, root := range roots {
			if err := stage.Remove(e.Catalog.Domain.Ref(base.Epoch(), root)); err != nil {
				t.Fatal(err)
			}
		}
		before := stage.RemovalCount()
		edges, err := view.DropClosure(core.StageDropProvider(context.Background(), stage, nil, nil, nil), roots)
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		for _, edge := range edges {
			if edge.Address == consumer {
				found++
				if edge.Target != overlap || edge.Automatic || !edge.Normal {
					t.Fatal("view lost its first genuine operator dependency parent", edge, overlap)
				}
			}
		}
		if found != 1 || stage.RemovalCount() != before {
			t.Fatal("closure changed retirement authority or repeated the consumer", found, stage.RemovalCount(), before)
		}
		if e.CatalogRead().State() != base.State() || !slices.Equal(seg.Members, segMembers) || !slices.Equal(unaccent.Members, unaccentMembers) {
			t.Fatal("unpublished retirement changed the accepted graph or manifests")
		}
	})
}
