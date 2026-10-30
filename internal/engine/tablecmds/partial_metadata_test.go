package tablecmds_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPartialIndexMetadataPredicateCapture(t *testing.T) {
	f, _, _, view := partialMaskFixture(t)
	ctx := context.Background()
	capture := view.Data().Catalog
	rows := view.Data().Committed[f.parent.Address()]
	oldRoot := f.e.Storage.IndexRoots[f.index.Address()]
	ref := f.e.Catalog.Domain.Ref(capture.Epoch(), f.index.Address())
	retained, _, err := index.ProjectIndexMetadata(ctx, capture, ref)
	if err != nil {
		t.Fatal(err)
	}
	consume := func(label string, metadata index.Metadata, want []plan.Truth, membership map[ids.RowID]ids.VersionID) {
		t.Helper()
		if metadata.Predicate == nil {
			t.Fatalf("%s: structural metadata omitted the partial predicate", label)
		}
		gotMembership := make(map[ids.RowID]ids.VersionID)
		for i, row := range rows {
			value, err := metadata.Predicate.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: row.Values}})
			if err != nil {
				t.Fatalf("%s row %d: %v", label, row.Row, err)
			}
			truth, err := plan.TruthOf(value)
			if err != nil || truth != want[i] {
				t.Fatalf("%s row %d: truth=%v want=%v error=%v", label, row.Row, truth, want[i], err)
			}
			if truth == plan.True {
				gotMembership[row.Row] = row.Version
			}
		}
		if !reflect.DeepEqual(gotMembership, membership) {
			t.Fatalf("%s: predicate members=%v want=%v", label, gotMembership, membership)
		}
	}
	oldTruth := []plan.Truth{plan.True, plan.False, plan.Unknown, plan.True, plan.True}
	oldMembers := map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5}
	consume("initial projection", retained, oldTruth, oldMembers)
	partialWantMembership(t, oldRoot, oldMembers)

	// Publish a detached definition and rebuilt root, leaving the captured
	// catalog, analyzed predicate and committed row vector in their old meaning.
	raw, err := parser.Parse("CREATE INDEX pi ON t(k) WHERE NOT active")
	if err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	bindingOp := &session.Operation{Ctx: ctx}
	bindingAuth := core.AuthorizationView{View: access.View{Catalog: capture}}
	transformed, err := tablecmds.TransformIndexPredicate(f.e, bindingOp, sqltype.InputEnvironment{}, bindingAuth, f.parent, raw[0].(*parser.CreateIndex).Predicate)
	var predicate *catalog.IndexPredicate
	if err == nil {
		predicate, err = tablecmds.CompleteIndexPredicate(bindingOp, bindingAuth, f.parent, transformed)
	}
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	definition := *f.index.SQLIndex
	definition.Predicate = predicate
	idx := *f.index
	idx.ObjectRevision++
	idx.SQLIndex = &definition
	next := catalog.CloneState(capture.State())
	next.SetDescriptor(idx.Address(), &idx)
	candidate := f.e.Catalog.Read(next)
	if err := index.ValidateIndexDefinition(candidate, &idx); err != nil {
		t.Fatal(err)
	}
	root, err := index.BuildIndexRoot(&exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}, f.parent, &definition, f.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	f.e.Catalog.PublishLocked(next)
	f.e.Storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{idx.Address(): root})
	f.e.Mu.Unlock()
	current, _, err := index.ProjectIndexMetadata(ctx, f.e.CatalogRead(), ref)
	if err != nil {
		t.Fatal(err)
	}
	consume("current projection", current, []plan.Truth{plan.False, plan.True, plan.Unknown, plan.False, plan.False}, map[ids.RowID]ids.VersionID{2: 2})
	partialWantMembership(t, root, map[ids.RowID]ids.VersionID{2: 2})
	consume("retained projection", retained, oldTruth, oldMembers)
	historical, _, err := index.ProjectIndexMetadata(ctx, capture, ref)
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
	f.e.Mu.Lock()
	bindingOp := &session.Operation{Ctx: ctx}
	bindingAuth := core.AuthorizationView{View: access.View{Catalog: capture}}
	transformed, err := tablecmds.TransformIndexPredicate(f.e, bindingOp, sqltype.InputEnvironment{}, bindingAuth, f.parent, raw[0].(*parser.CreateIndex).Predicate)
	var predicate *catalog.IndexPredicate
	if err == nil {
		predicate, err = tablecmds.CompleteIndexPredicate(bindingOp, bindingAuth, f.parent, transformed)
	}
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	// Decisive FALSE excludes every row; structural consumers must still
	// recover the original attribute reference even after simplification.
	definition := *f.index.SQLIndex
	definition.Predicate = predicate
	idx := *f.index
	idx.SQLIndex = &definition
	next := catalog.CloneState(capture.State())
	next.SetDescriptor(idx.Address(), &idx)
	candidate := f.e.Catalog.Read(next)
	if err := index.ValidateIndexDefinition(candidate, &idx); err != nil {
		t.Fatal(err)
	}
	metadata, _, err := index.ProjectIndexMetadata(ctx, candidate, f.e.Catalog.Domain.Ref(candidate.Epoch(), idx.Address()))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Predicate == nil {
		t.Fatal("structural metadata omitted the original analyzed predicate")
	}
	var dependencies []ids.ObjectRef
	if _, err := (stored.ScalarRenderer{Owner: f.parent, Dependencies: &dependencies}).SQL(ctx, candidate, metadata.Predicate); err != nil {
		t.Fatal(err)
	}
	var attributes []ids.ObjectRef
	for _, dependency := range dependencies {
		if dependency.Address.ClassID == ids.RelationClassID {
			attributes = append(attributes, dependency)
		}
	}
	wantRef := f.e.Catalog.Domain.Ref(candidate.Epoch(), f.parent.Address())
	wantRef.Address.SubID = 20
	if !reflect.DeepEqual(attributes, []ids.ObjectRef{wantRef}) {
		t.Fatalf("original predicate relation dependencies=%v want attribute reference=%v", attributes, wantRef)
	}
	var preparation index.ExecutionPreparation
	for _, row := range f.store.Versions {
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Catalog: operatorFamilyTestRead(), Context: ctx}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: row.Values}}
		value, err := metadata.Predicate.Eval(&evaluation)
		if err != nil {
			t.Fatalf("row %d: %v", row.Row, err)
		}
		truth, err := plan.TruthOf(value)
		if err != nil || truth != plan.False {
			t.Fatalf("row %d: original FALSE AND active truth=%v error=%v", row.Row, truth, err)
		}
		matched, err := preparation.PredicateMatches(&evaluation, f.parent, &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Predicate: predicate}, row.Values, nil, nil)
		if err != nil || matched {
			t.Fatalf("row %d: simplified execution admitted a member: %v %v", row.Row, matched, err)
		}
	}
}

func partialMaskFixture(t *testing.T) (partialIndexFixture, *core.StatementScope, exec.Leaf, *visibility.View) {
	f := newPartialIndexFixture(t)
	scope := &core.StatementScope{Owner: &session.TransactionOwner{Isolation: session.ReadCommitted}}
	leaf := exec.Leaf{Ref: f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.parent.Address()), Relation: f.parent, Storage: f.store, PartialCandidate: f.index}
	data := &visibility.Data{Catalog: f.e.CatalogRead(), Frontier: f.e.Visibility.CommitSeq(), Committed: map[ids.ObjectAddress][]storage.RowVersion{f.parent.Address(): f.store.Versions}, SingleGeneration: f.store}
	view := visibility.NewView(data)
	t.Cleanup(view.Release)
	return f, scope, leaf, view
}

func partialWantMembership(t *testing.T, root *storage.IndexRoot, want map[ids.RowID]ids.VersionID) {
	t.Helper()
	if !reflect.DeepEqual(root.Membership, want) || len(root.Entries) != len(want) {
		t.Fatal("wrong membership", root.Membership, want, root.Entries)
	}
	for _, entry := range root.Entries {
		if root.Membership[entry.Row] != entry.Version {
			t.Fatal("entry/lookup disagreement", entry)
		}
	}
}
