package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/stored"
)

func infoViewColumnUsageCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	target := sequence.NewRelation(sequence.RelationSpec{Address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20300}, Namespace: f.schema, Name: "captured_table", Revision: 1, Owner: f.owner, Columns: sequence.Columns(f.e.CatalogRead())})
	target.RelationKind = catalog.TableRelation
	target.TableMethod = 2
	state.SetDescriptor(target.Address(), target)
	v := *target
	v.ObjectAddress.ObjectID++
	v.ObjectName, v.RelationKind = "captured_view", catalog.ViewRelation
	address := target.Address()
	address.SubID = 3
	ref := f.e.Catalog.Domain.Ref(state.Epoch(), address)
	v.View = stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: target.Columns[2].Usage}, Origin: ref}}}}, Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: 1}}})
	state.SetDescriptor(v.Address(), &v)
	f.e.Catalog.PublishLocked(state)
	f.op.Session.DatabaseName = "physical_capture_database"
	f.actor(f.owner)
	scope, err := f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	f.e.Mu.Lock()
	err = f.e.CaptureCommentSnapshotLocked(scope)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := scope.Inquiry.CommentCatalog
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoViewColumnUsageOID})
	// Change the published root after capture. The metadata source must use
	// the paired statement frontier, not acquire a fresh catalog halfway in.
	next := catalog.CloneState(state)
	renamed := *target
	renamed.ObjectName = "later_name"
	renamed.Columns = append([]catalog.Column(nil), target.Columns...)
	renamed.Columns[2].Name = "later_column"
	next.SetDescriptor(renamed.Address(), &renamed)
	f.e.Catalog.PublishLocked(next)
	f.e.Mu.Lock()
	captured, err := core.EngineOpenRelationSourceLocked(f.e, scope, view.Owner().Domain.Ref(view.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil || captured == nil {
		t.Fatal("coherent captured usage source", err)
	}
	defer captured.Close()
	want := []string{"physical_capture_database", "default_schema", "captured_view", "physical_capture_database", "default_schema", "captured_table", "is_called"}
	if len(captured.Anonymous) != 1 || len(captured.Anonymous[0]) != 7 {
		t.Fatal("captured pair shape", captured.Anonymous)
	}
	for i, text := range want {
		if got := infoUserMappingsUnitText(t, captured.Anonymous[0][i]); got != text {
			t.Fatal("paired statement frontier", i, got, text)
		}
	}
	for i, cell := range captured.Anonymous[0] {
		column := source.Columns[i]
		if cell.Type() != column.Usage.Type || column.OriginTable != uint32(pgcatalog.InfoViewColumnUsageOID) || column.OriginAttribute != int16(i+1) {
			t.Fatal("captured domain/origin", i, cell, column)
		}
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	// A fresh physical statement sees the published rename, while the
	// original captured row remains unchanged.
	scope, err = f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	f.e.Mu.Lock()
	err = f.e.CaptureCommentSnapshotLocked(scope)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	freshView := scope.Inquiry.CommentCatalog
	f.e.Mu.Lock()
	fresh, err := core.EngineOpenRelationSourceLocked(f.e, scope, freshView.Owner().Domain.Ref(freshView.Epoch(), source.Address()), source.Owner, plan.RowMark{}, nil, nil)
	if err != nil || fresh == nil {
		t.Fatal("fresh captured usage source", err)
	}
	defer fresh.Close()
	if len(fresh.Anonymous) != 1 || infoUserMappingsUnitText(t, fresh.Anonymous[0][5]) != "later_name" || infoUserMappingsUnitText(t, fresh.Anonymous[0][6]) != "later_column" {
		t.Fatal("fresh statement did not observe rename", fresh.Anonymous)
	}
	if infoUserMappingsUnitText(t, captured.Anonymous[0][5]) != "captured_table" || infoUserMappingsUnitText(t, captured.Anonymous[0][6]) != "is_called" {
		t.Fatal("retained capture mutated")
	}
}
