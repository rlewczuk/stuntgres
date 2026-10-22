package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func logicalGiSTFixture(t *testing.T) (*catalog.Relation, *catalog.Relation, func(ids.RowID, string, string) storage.RowVersion) {
	t.Helper()
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	inet, r := types[sqltype.Inet], types[sqltype.Int4Range]
	parent := &catalog.Relation{ObjectName: "bookings", Attributes: []int32{1, 2}, Columns: []catalog.Column{{Name: "tenant", Usage: inet.Usage()}, {Name: "during", Usage: r.Usage()}}}
	ic, _ := sqltype.LookupGiSTIndexClass("inet_ops")
	rc, _ := sqltype.LookupGiSTIndexClass("range_ops")
	eq, err := operatorFamilyTestExclusion(inet.Usage(), ic, "=")
	if err != nil {
		t.Fatal(err)
	}
	overlap, err := operatorFamilyTestExclusion(r.Usage(), rc, "&&")
	if err != nil {
		t.Fatal(err)
	}
	index := &catalog.Relation{ObjectName: "booking_ex", SQLIndex: &catalog.IndexDefinition{Method: catalog.GistMethod, MethodOID: 783, Keys: []catalog.IndexKey{{Attribute: 1, Class: ic}, {Attribute: 2, Class: rc}}, Exclusion: []sqltype.ExclusionOperator{eq, overlap}, Ready: true, Live: true, Valid: true}}
	index.SQLIndex.Constraint = &catalog.BackingConstraint{Kind: catalog.ExclusionBackingKind, OID: 1, Name: index.Name()}
	row := func(id ids.RowID, tenant, during string) storage.RowVersion {
		a, err := inet.Input(context.Background(), tenant, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.Input(context.Background(), during, -1)
		if err != nil {
			t.Fatal(err)
		}
		return storage.RowVersion{Row: id, Version: ids.VersionID(id), Born: 1, Values: []sqltype.Value{a, b}}
	}
	return parent, index, row
}

func TestLogicalGiSTNonadjacentOverlap(t *testing.T) {
	parent, idx, row := logicalGiSTFixture(t)
	for _, middle := range []storage.RowVersion{row(2, "10.0.0.1", "empty"), row(2, "10.0.0.2", "[1,5)")} {
		store := &storage.Relation{Versions: []storage.RowVersion{row(1, "10.0.0.1", "[1,5)"), middle, row(3, "10.0.0.1", "[3,7)")}}
		root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}}, parent, idx.SQLIndex, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		if root.Hash != nil || len(root.Entries) != 3 || root.Entries[1].Row != 2 {
			t.Fatal("membership reordered", root)
		}
		before := append([]storage.IndexEntry(nil), root.Entries...)
		err = index.ValidateExclusionRoot(context.Background(), parent, idx, root, "public", sqltype.OutputEnvironment{}, &access.View{Catalog: operatorFamilyTestRead()}, nil, true)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" {
			t.Fatal("nonadjacent overlap not rejected", err)
		}
		if !reflect.DeepEqual(before, root.Entries) {
			t.Fatal("validation mutated source")
		}
	}
}

func TestLogicalGiSTDeltaImmutable(t *testing.T) {
	parent, idx, row := logicalGiSTFixture(t)
	store := &storage.Relation{Versions: []storage.RowVersion{row(1, "10.0.0.1", "[1,5)"), row(2, "10.0.0.2", "[1,5)")}}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}}
	root, err := index.BuildIndexRoot(evaluation, parent, idx.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]storage.IndexEntry(nil), root.Entries...)
	publication := &index.RowPublication{Writes: []storage.RowVersion{row(3, "10.0.0.1", "[3,7)")}, SingleSlot: -1}
	_, err = index.MergeIndexRoot(evaluation, parent, idx, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" {
		t.Fatal("delta overlap not rejected", err)
	}
	replacement := row(1, "10.0.0.1", "[5,8)")
	replacement.Base = 1
	replacement.Version = 4
	publication.Writes = []storage.RowVersion{replacement}
	next, err := index.MergeIndexRoot(evaluation, parent, idx, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil, nil)
	if err != nil || next == nil || len(next.Entries) != 2 || next.Entries[1].Version != 4 {
		t.Fatal("replacement", next, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = index.MergeIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: canceled}}, parent, idx, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, root.Entries) {
		t.Fatal("delta/cancellation mutated source")
	}
}

func TestLogicalGiSTRebuildPublicationNonadjacentOverlap(t *testing.T) {
	for _, materialize := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepare", true: "materialize"}[materialize], func(t *testing.T) {
			e := New()
			parent, idx, row := logicalGiSTFixture(t)
			parent.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
			parent.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			parent.RelationKind = catalog.TableRelation
			idx.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
			idx.ObjectNamespace = parent.Namespace()
			idx.RelationKind = catalog.IndexRelation
			idx.Columns = append([]catalog.Column(nil), parent.Columns...)
			idx.SQLIndex.Parent = parent.Address()
			parent.Constraints.Exclusion = []catalog.ExclusionConstraint{{
				OID: idx.SQLIndex.Constraint.OID, Name: idx.Name(), Index: idx.Address(),
				IsLocal: true, Operators: idx.SQLIndex.Exclusion,
			}}
			// All stored values and bound operators use the same descriptor
			// authority; mixing a fresh Builtins set with engine-owned values
			// would fail equality before reaching collection validation.
			e.catalog.state = catalog.CloneState(e.catalog.state)
			e.catalog.state.SetDescriptor(parent.Address(), parent)
			e.catalog.state.SetDescriptor(idx.Address(), idx)
			store := &storage.Relation{Versions: []storage.RowVersion{
				row(1, "10.0.0.1", "[1,5)"),
				row(2, "10.0.0.1", "empty"),
			}}
			root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}}, parent, idx.SQLIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			e.storage.InstallRelationLocked(parent.Address(), store)
			e.mu.Unlock()
			e.mu.Lock()
			e.storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): root})
			e.mu.Unlock()
			before := indexMaintenanceCapture(e)
			storeBefore := uniquePublicationStore(store)
			entriesBefore := append([]storage.IndexEntry(nil), root.Entries...)
			for i := range entriesBefore {
				entriesBefore[i].Keys = append([]sqltype.Value(nil), entriesBefore[i].Keys...)
			}
			rootBefore := *root
			// The candidate extends the same typed private fixture, not a
			// separately initialized SQL catalog.
			captured := index.CaptureIndexStores(e.storage.Relations, e.catalog.state)
			candidate := captured[parent.Address()]
			candidate.Versions = append(append([]storage.RowVersion(nil), candidate.Versions...), row(3, "10.0.0.1", "[3,7)"))
			candidateBefore := uniquePublicationStore(candidate)
			// Explicit parent rebuild intent, no supplied candidate root and no
			// row delta: validation must inspect rebuilt A/empty/C membership.
			roots, err := e.prepareIndexRoots(
				&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}},
				e.catalog.state, e.catalog.state, captured, e.storage.IndexRoots,
				map[ids.ObjectAddress]bool{parent.Address(): true}, nil, nil,
				materialize, sqltype.OutputEnvironment{}, nil, nil, nil,
			)
			indexMaintenanceUnchanged(t, e, before)
			if !reflect.DeepEqual(*store, storeBefore) || !reflect.DeepEqual(*candidate, candidateBefore) ||
				!reflect.DeepEqual(*root, rootBefore) || !reflect.DeepEqual(root.Entries, entriesBefore) {
				t.Fatal("failed rebuild changed original storage, candidate rows or root entries")
			}
			var diagnostic *sqlstate.Error
			if roots != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" || diagnostic.Constraint != "booking_ex" {
				t.Fatal("rebuild publication accepted nonadjacent GiST overlap", roots, err)
			}
			if diagnostic.Message != `could not create exclusion constraint "booking_ex"` {
				t.Fatal("rebuild did not report a constraint-build failure", diagnostic)
			}
		})
	}
}

func TestLogicalGiSTUnchangedPublicationNonadjacentOverlap(t *testing.T) {
	for _, materialize := range []bool{false, true} {
		t.Run(map[bool]string{false: "prepare", true: "materialize"}[materialize], func(t *testing.T) {
			parent, idx, row := logicalGiSTFixture(t)
			store := &storage.Relation{Versions: []storage.RowVersion{
				row(1, "10.0.0.1", "[1,5)"),
				row(2, "10.0.0.1", "empty"),
				row(3, "10.0.0.1", "[3,7)"),
			}}
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: context.Background()}}
			root, err := index.BuildIndexRoot(evaluation, parent, idx.SQLIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			entriesBefore := append([]storage.IndexEntry(nil), root.Entries...)
			for i := range entriesBefore {
				entriesBefore[i].Keys = append([]sqltype.Value(nil), entriesBefore[i].Keys...)
			}
			rootBefore := *root
			storeBefore := uniquePublicationStore(store)
			// There are deliberately no writes or deletions: exercise the
			// unchanged-membership validation entrance, not the GiST delta path.
			publication := &index.RowPublication{SingleSlot: -1}
			next, err := index.MergeIndexRoot(evaluation, parent, idx, root.Entries, publication, false, materialize, false, "public", sqltype.OutputEnvironment{}, nil, nil)
			if !reflect.DeepEqual(*root, rootBefore) || !reflect.DeepEqual(root.Entries, entriesBefore) ||
				!reflect.DeepEqual(*store, storeBefore) {
				t.Fatal("unchanged publication mutated original root entries or storage")
			}
			var diagnostic *sqlstate.Error
			if next != nil || !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" || diagnostic.Constraint != "booking_ex" {
				t.Fatal("unchanged publication accepted nonadjacent GiST overlap", next, err)
			}
			if diagnostic.Message != `conflicting key value violates exclusion constraint "booking_ex"` {
				t.Fatal("unchanged publication did not report a live integrity failure", diagnostic)
			}
		})
	}
}
