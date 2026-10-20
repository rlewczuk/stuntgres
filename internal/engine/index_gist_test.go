package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	eq, err := sqltype.BindExclusionOperator(inet.Usage(), ic, "=")
	if err != nil {
		t.Fatal(err)
	}
	overlap, err := sqltype.BindExclusionOperator(r.Usage(), rc, "&&")
	if err != nil {
		t.Fatal(err)
	}
	index := &catalog.Relation{ObjectName: "booking_ex", SQLIndex: &catalog.IndexDefinition{Method: catalog.GistMethod, Keys: []catalog.IndexKey{{Attribute: 1, Class: ic}, {Attribute: 2, Class: rc}}, Exclusion: []sqltype.ExclusionOperator{eq, overlap}, Ready: true, Live: true, Valid: true}}
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
	parent, index, row := logicalGiSTFixture(t)
	for _, middle := range []storage.RowVersion{row(2, "10.0.0.1", "empty"), row(2, "10.0.0.2", "[1,5)")} {
		store := &storage.Relation{Versions: []storage.RowVersion{row(1, "10.0.0.1", "[1,5)"), middle, row(3, "10.0.0.1", "[3,7)")}}
		root, err := buildIndexRoot(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, parent, index.SQLIndex, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		if root.Hash != nil || len(root.Entries) != 3 || root.Entries[1].Row != 2 {
			t.Fatal("membership reordered", root)
		}
		before := append([]storage.IndexEntry(nil), root.Entries...)
		err = validateExclusionRoot(context.Background(), parent, index, root, "public", sqltype.OutputEnvironment{}, nil, true)
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
	parent, index, row := logicalGiSTFixture(t)
	store := &storage.Relation{Versions: []storage.RowVersion{row(1, "10.0.0.1", "[1,5)"), row(2, "10.0.0.2", "[1,5)")}}
	evaluation := &EvalContext{EvalStatement: evalStatement{Context: context.Background()}}
	root, err := buildIndexRoot(evaluation, parent, index.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]storage.IndexEntry(nil), root.Entries...)
	publication := &rowPublication{writes: []storage.RowVersion{row(3, "10.0.0.1", "[3,7)")}, singleSlot: -1}
	_, err = mergeIndexRoot(evaluation, parent, index, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" {
		t.Fatal("delta overlap not rejected", err)
	}
	replacement := row(1, "10.0.0.1", "[5,8)")
	replacement.Base = 1
	replacement.Version = 4
	publication.writes = []storage.RowVersion{replacement}
	next, err := mergeIndexRoot(evaluation, parent, index, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil)
	if err != nil || next == nil || len(next.Entries) != 2 || next.Entries[1].Version != 4 {
		t.Fatal("replacement", next, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = mergeIndexRoot(&EvalContext{EvalStatement: evalStatement{Context: canceled}}, parent, index, root.Entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil)
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
			parent, index, row := logicalGiSTFixture(t)
			parent.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}
			parent.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
			parent.RelationKind = catalog.TableRelation
			index.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
			index.ObjectNamespace = parent.Namespace()
			index.RelationKind = catalog.IndexRelation
			index.Columns = append([]catalog.Column(nil), parent.Columns...)
			index.SQLIndex.Parent = parent.Address()
			parent.Constraints.Exclusion = []catalog.ExclusionConstraint{{
				OID: index.SQLIndex.Constraint.OID, Name: index.Name(), Index: index.Address(),
				IsLocal: true, Operators: index.SQLIndex.Exclusion,
			}}
			// All stored values and bound operators use the same descriptor
			// authority; mixing a fresh Builtins set with engine-owned values
			// would fail equality before reaching collection validation.
			e.catalog.state = catalog.CloneState(e.catalog.state)
			e.catalog.state.SetDescriptor(parent.Address(), parent)
			e.catalog.state.SetDescriptor(index.Address(), index)
			store := &storage.Relation{Versions: []storage.RowVersion{
				row(1, "10.0.0.1", "[1,5)"),
				row(2, "10.0.0.1", "empty"),
			}}
			root, err := buildIndexRoot(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}}, parent, index.SQLIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			e.storage.InstallRelationLocked(parent.Address(), store)
			e.mu.Unlock()
			e.mu.Lock()
			e.storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{index.Address(): root})
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
			captured := captureIndexStores(e.storage.Relations, e.catalog.state)
			candidate := captured[parent.Address()]
			candidate.Versions = append(append([]storage.RowVersion(nil), candidate.Versions...), row(3, "10.0.0.1", "[3,7)"))
			candidateBefore := uniquePublicationStore(candidate)
			// Explicit parent rebuild intent, no supplied candidate root and no
			// row delta: validation must inspect rebuilt A/empty/C membership.
			roots, err := e.prepareIndexPublication(
				&EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 1, Frame: 1}},
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
			parent, index, row := logicalGiSTFixture(t)
			store := &storage.Relation{Versions: []storage.RowVersion{
				row(1, "10.0.0.1", "[1,5)"),
				row(2, "10.0.0.1", "empty"),
				row(3, "10.0.0.1", "[3,7)"),
			}}
			evaluation := &EvalContext{EvalStatement: evalStatement{Context: context.Background()}}
			root, err := buildIndexRoot(evaluation, parent, index.SQLIndex, store, nil)
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
			publication := &rowPublication{singleSlot: -1}
			next, err := mergeIndexRoot(evaluation, parent, index, root.Entries, publication, false, materialize, false, "public", sqltype.OutputEnvironment{}, nil)
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
