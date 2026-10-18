package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func logicalGiSTFixture(t *testing.T) (*relationDescriptor, *relationDescriptor, func(ids.RowID, string, string) rowVersion) {
	t.Helper()
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	inet, r := types[sqltype.Inet], types[sqltype.Int4Range]
	parent := &relationDescriptor{ObjectName: "bookings", Attributes: []int32{1, 2}, Columns: []ColumnDescriptor{{Name: "tenant", Usage: inet.Usage()}, {Name: "during", Usage: r.Usage()}}}
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
	index := &relationDescriptor{ObjectName: "booking_ex", SQLIndex: &indexDefinition{Method: gistMethod, Keys: []indexKey{{Attribute: 1, Class: ic}, {Attribute: 2, Class: rc}}, Exclusion: []sqltype.ExclusionOperator{eq, overlap}, Ready: true, Live: true, Valid: true}}
	index.SQLIndex.Constraint = &backingConstraint{Kind: exclusionBackingKind, OID: 1, Name: index.Name()}
	row := func(id ids.RowID, tenant, during string) rowVersion {
		a, err := inet.Input(context.Background(), tenant, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.Input(context.Background(), during, -1)
		if err != nil {
			t.Fatal(err)
		}
		return rowVersion{row: id, version: ids.VersionID(id), born: 1, values: []sqltype.Value{a, b}}
	}
	return parent, index, row
}

func TestLogicalGiSTNonadjacentOverlap(t *testing.T) {
	parent, index, row := logicalGiSTFixture(t)
	for _, middle := range []rowVersion{row(2, "10.0.0.1", "empty"), row(2, "10.0.0.2", "[1,5)")} {
		store := &relationStorage{versions: []rowVersion{row(1, "10.0.0.1", "[1,5)"), middle, row(3, "10.0.0.1", "[3,7)")}}
		root, err := buildIndexRoot(&EvalContext{Context: context.Background()}, parent, index.SQLIndex, store, nil)
		if err != nil {
			t.Fatal(err)
		}
		if root.hash != nil || len(root.entries) != 3 || root.entries[1].row != 2 {
			t.Fatal("membership reordered", root)
		}
		before := append([]logicalIndexEntry(nil), root.entries...)
		err = validateExclusionRoot(context.Background(), parent, index, root, "public", sqltype.OutputEnvironment{}, nil, true)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" {
			t.Fatal("nonadjacent overlap not rejected", err)
		}
		if !reflect.DeepEqual(before, root.entries) {
			t.Fatal("validation mutated source")
		}
	}
}

func TestLogicalGiSTDeltaImmutable(t *testing.T) {
	parent, index, row := logicalGiSTFixture(t)
	store := &relationStorage{versions: []rowVersion{row(1, "10.0.0.1", "[1,5)"), row(2, "10.0.0.2", "[1,5)")}}
	evaluation := &EvalContext{Context: context.Background()}
	root, err := buildIndexRoot(evaluation, parent, index.SQLIndex, store, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]logicalIndexEntry(nil), root.entries...)
	publication := &rowPublication{writes: []rowVersion{row(3, "10.0.0.1", "[3,7)")}, singleSlot: -1}
	_, err = mergeIndexRoot(evaluation, parent, index, root.entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23P01" {
		t.Fatal("delta overlap not rejected", err)
	}
	replacement := row(1, "10.0.0.1", "[5,8)")
	replacement.base = 1
	replacement.version = 4
	publication.writes = []rowVersion{replacement}
	next, err := mergeIndexRoot(evaluation, parent, index, root.entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil)
	if err != nil || next == nil || len(next.entries) != 2 || next.entries[1].version != 4 {
		t.Fatal("replacement", next, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = mergeIndexRoot(&EvalContext{Context: canceled}, parent, index, root.entries, publication, true, true, false, "public", sqltype.OutputEnvironment{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, root.entries) {
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
			parent.RelationKind = tableRelation
			index.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001}
			index.ObjectNamespace = parent.Namespace()
			index.RelationKind = indexRelation
			index.Columns = append([]ColumnDescriptor(nil), parent.Columns...)
			index.SQLIndex.Parent = parent.Address()
			parent.ExclusionConstraints = []exclusionConstraint{{
				OID: index.SQLIndex.Constraint.OID, Name: index.Name(), Index: index.Address(),
				IsLocal: true, Operators: index.SQLIndex.Exclusion,
			}}
			// All stored values and bound operators use the same descriptor
			// authority; mixing a fresh Builtins set with engine-owned values
			// would fail equality before reaching collection validation.
			e.catalog.state = cloneCatalog(e.catalog.state)
			e.catalog.state.setDescriptor(parent.Address(), parent)
			e.catalog.state.setDescriptor(index.Address(), index)
			store := &relationStorage{versions: []rowVersion{
				row(1, "10.0.0.1", "[1,5)"),
				row(2, "10.0.0.1", "empty"),
			}}
			root, err := buildIndexRoot(&EvalContext{Context: context.Background()}, parent, index.SQLIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			e.storage[parent.Address()] = store
			e.indexRoots = map[ids.ObjectAddress]*logicalIndexRoot{index.Address(): root}
			before := indexMaintenanceCapture(e)
			storeBefore := uniquePublicationStore(store)
			entriesBefore := append([]logicalIndexEntry(nil), root.entries...)
			for i := range entriesBefore {
				entriesBefore[i].keys = append([]sqltype.Value(nil), entriesBefore[i].keys...)
			}
			rootBefore := *root
			// The candidate extends the same typed private fixture, not a
			// separately initialized SQL catalog.
			captured := captureIndexStores(e.storage, e.catalog.state)
			candidate := captured[parent.Address()]
			candidate.versions = append(append([]rowVersion(nil), candidate.versions...), row(3, "10.0.0.1", "[3,7)"))
			candidateBefore := uniquePublicationStore(candidate)
			// Explicit parent rebuild intent, no supplied candidate root and no
			// row delta: validation must inspect rebuilt A/empty/C membership.
			roots, err := e.prepareIndexPublication(
				&EvalContext{Context: context.Background(), Scope: 1, Frame: 1},
				e.catalog.state, e.catalog.state, captured, e.indexRoots,
				map[ids.ObjectAddress]bool{parent.Address(): true}, nil, nil,
				materialize, sqltype.OutputEnvironment{}, nil, nil, nil,
			)
			indexMaintenanceUnchanged(t, e, before)
			if !reflect.DeepEqual(*store, storeBefore) || !reflect.DeepEqual(*candidate, candidateBefore) ||
				!reflect.DeepEqual(*root, rootBefore) || !reflect.DeepEqual(root.entries, entriesBefore) {
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
			store := &relationStorage{versions: []rowVersion{
				row(1, "10.0.0.1", "[1,5)"),
				row(2, "10.0.0.1", "empty"),
				row(3, "10.0.0.1", "[3,7)"),
			}}
			evaluation := &EvalContext{Context: context.Background()}
			root, err := buildIndexRoot(evaluation, parent, index.SQLIndex, store, nil)
			if err != nil {
				t.Fatal(err)
			}
			entriesBefore := append([]logicalIndexEntry(nil), root.entries...)
			for i := range entriesBefore {
				entriesBefore[i].keys = append([]sqltype.Value(nil), entriesBefore[i].keys...)
			}
			rootBefore := *root
			storeBefore := uniquePublicationStore(store)
			// There are deliberately no writes or deletions: exercise the
			// unchanged-membership validation entrance, not the GiST delta path.
			publication := &rowPublication{singleSlot: -1}
			next, err := mergeIndexRoot(evaluation, parent, index, root.entries, publication, false, materialize, false, "public", sqltype.OutputEnvironment{}, nil)
			if !reflect.DeepEqual(*root, rootBefore) || !reflect.DeepEqual(root.entries, entriesBefore) ||
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
