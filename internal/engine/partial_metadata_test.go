package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPartialIndexMetadataPredicateCapture(t *testing.T) {
	f, _, _, view := partialMaskFixture(t)
	ctx := context.Background()
	capture := view.read().catalog
	rows := view.read().committed[f.parent.address]
	oldRoot := f.e.indexRoots[f.index.address]
	ref := ObjectRef{owner: f.e.owner, Epoch: capture.Epoch(), Address: f.index.address}
	retained, _, err := projectIndexMetadata(ctx, capture, ref)
	if err != nil {
		t.Fatal(err)
	}
	consume := func(label string, metadata indexMetadata, want []Truth, membership map[RowID]VersionID) {
		t.Helper()
		if metadata.Predicate == nil {
			t.Fatalf("%s: structural metadata omitted the partial predicate", label)
		}
		gotMembership := make(map[RowID]VersionID)
		for i, row := range rows {
			value, err := metadata.Predicate.Eval(&EvalContext{Context: ctx, Scope: 1, Frame: 1, Row: row.values})
			if err != nil {
				t.Fatalf("%s row %d: %v", label, row.row, err)
			}
			truth, err := TruthOf(value)
			if err != nil || truth != want[i] {
				t.Fatalf("%s row %d: truth=%v want=%v error=%v", label, row.row, truth, want[i], err)
			}
			if truth == True {
				gotMembership[row.row] = row.version
			}
		}
		if !reflect.DeepEqual(gotMembership, membership) {
			t.Fatalf("%s: predicate members=%v want=%v", label, gotMembership, membership)
		}
	}
	oldTruth := []Truth{True, False, Unknown, True, True}
	oldMembers := map[RowID]VersionID{1: 1, 4: 4, 5: 5}
	consume("initial projection", retained, oldTruth, oldMembers)
	partialWantMembership(t, oldRoot, oldMembers)

	// Publish a detached definition and rebuilt root, leaving the captured
	// catalog, analyzed predicate and committed row vector in their old meaning.
	raw, err := parser.Parse("CREATE INDEX pi ON t(k) WHERE NOT active")
	if err != nil {
		t.Fatal(err)
	}
	f.e.mu.Lock()
	predicate, err := f.e.bindIndexPredicate(&Operation{ctx: ctx}, sqltype.InputEnvironment{}, AuthorizationView{catalog: capture}, f.parent, raw[0].(*parser.CreateIndex).Predicate)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	definition := *f.index.sqlIndex
	definition.predicate = predicate
	index := *f.index
	index.revision++
	index.sqlIndex = &definition
	next := cloneCatalog(capture.state)
	next.descriptors[index.address] = &index
	candidate := CatalogRead{owner: f.e, state: next}
	if err := validateIndexDefinition(candidate, &index); err != nil {
		t.Fatal(err)
	}
	root, err := buildIndexRoot(ctx, f.parent, &definition, f.store)
	if err != nil {
		t.Fatal(err)
	}
	f.e.mu.Lock()
	f.e.state = next
	f.e.indexRoots = map[ObjectAddress]*logicalIndexRoot{index.address: root}
	f.e.mu.Unlock()
	current, _, err := projectIndexMetadata(ctx, f.e.CatalogRead(), ref)
	if err != nil {
		t.Fatal(err)
	}
	consume("current projection", current, []Truth{False, True, Unknown, False, False}, map[RowID]VersionID{2: 2})
	partialWantMembership(t, root, map[RowID]VersionID{2: 2})
	consume("retained projection", retained, oldTruth, oldMembers)
	historical, _, err := projectIndexMetadata(ctx, capture, ref)
	if err != nil {
		t.Fatal(err)
	}
	consume("historical reprojection", historical, oldTruth, oldMembers)
	partialWantMembership(t, oldRoot, oldMembers)
}

func TestPartialIndexMetadataPredicateOriginalDependencies(t *testing.T) {
	f := newPartialIndexFixture(t)
	ctx := context.Background()
	capture := f.e.CatalogRead()
	raw, err := parser.Parse("CREATE INDEX pi ON t(k) WHERE FALSE AND active")
	if err != nil {
		t.Fatal(err)
	}
	f.e.mu.Lock()
	predicate, err := f.e.bindIndexPredicate(&Operation{ctx: ctx}, sqltype.InputEnvironment{}, AuthorizationView{catalog: capture}, f.parent, raw[0].(*parser.CreateIndex).Predicate)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Decisive FALSE excludes every row; structural consumers must still
	// recover the original attribute reference even after simplification.
	definition := *f.index.sqlIndex
	definition.predicate = predicate
	index := *f.index
	index.sqlIndex = &definition
	next := cloneCatalog(capture.state)
	next.descriptors[index.address] = &index
	candidate := CatalogRead{owner: f.e, state: next}
	if err := validateIndexDefinition(candidate, &index); err != nil {
		t.Fatal(err)
	}
	metadata, _, err := projectIndexMetadata(ctx, candidate, ObjectRef{owner: f.e.owner, Epoch: candidate.Epoch(), Address: index.address})
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Predicate == nil {
		t.Fatal("structural metadata omitted the original analyzed predicate")
	}
	var dependencies []ObjectRef
	if _, err := (scalarRenderer{owner: f.parent, dependencies: &dependencies}).sql(ctx, candidate, metadata.Predicate); err != nil {
		t.Fatal(err)
	}
	var attributes []ObjectRef
	for _, dependency := range dependencies {
		if dependency.Address.ClassID == RelationClassID {
			attributes = append(attributes, dependency)
		}
	}
	wantRef := ObjectRef{owner: f.e.owner, Epoch: candidate.Epoch(), Address: f.parent.address}
	wantRef.Address.SubID = 20
	if !reflect.DeepEqual(attributes, []ObjectRef{wantRef}) {
		t.Fatalf("original predicate relation dependencies=%v want attribute reference=%v", attributes, wantRef)
	}
	for _, row := range f.store.versions {
		evaluation := EvalContext{Context: ctx, Scope: 1, Frame: 1, Row: row.values}
		value, err := metadata.Predicate.Eval(&evaluation)
		if err != nil {
			t.Fatalf("row %d: %v", row.row, err)
		}
		truth, err := TruthOf(value)
		if err != nil || truth != False {
			t.Fatalf("row %d: original FALSE AND active truth=%v error=%v", row.row, truth, err)
		}
		matched, err := indexPredicateMatches(&evaluation, predicate, row.values)
		if err != nil || matched {
			t.Fatalf("row %d: simplified execution admitted a member: %v %v", row.row, matched, err)
		}
	}
}
