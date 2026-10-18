package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRegclassDropColumnStagedOwnerDefinitions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE rr_stage_owner(v INT DEFAULT 7,kept INT DEFAULT 11 CONSTRAINT rr_kept_positive CHECK(kept>0))",
		"CREATE VIEW rr_stage_view AS SELECT v FROM rr_stage_owner",
		"ALTER TABLE rr_stage_owner ADD COLUMN r regclass DEFAULT 'rr_stage_view' CONSTRAINT rr_stage_bound CHECK(r='rr_stage_view'::regclass)",
	} {
		roleExecute(t, e, op, query)
	}
	ref := roleTableRef(t, e, "rr_stage_owner")
	catalogView := e.CatalogRead()
	descriptor, _ := catalogView.Descriptor(ref.Address)
	old := descriptor.(*catalog.Relation)
	kept := old.Attributes[old.Index["kept"]]
	remaining := old.Defaults[kept]
	stage := e.BeginCatalogStage(catalogView)
	defer e.DiscardCatalogStage(stage)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(nil)
	changes := newCatalogChanges()
	next, err := scope.dropColumnCandidate(stage, old, ref, parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: "v"}, Mode: parser.DropCascade}, changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Columns) != 3 || len(old.Defaults) != 3 || len(old.CheckConstraints) != 2 {
		t.Fatal("original descriptor mutated")
	}
	if len(next.Columns) != 2 || next.Index["kept"] != 0 || next.Index["r"] != 1 || !reflect.DeepEqual(next.Attributes, old.Attributes[1:]) || len(next.Defaults) != 1 || next.Defaults[kept].OID != remaining.OID || remaining.OID == 0 {
		t.Fatal("staged definitions or stable shape lost", next.Columns, next.Attributes, next.Defaults)
	}
	if len(next.CheckConstraints) != 1 || next.CheckConstraints[0].Name != "rr_kept_positive" || stage.Descriptors[ref.Address] != next {
		t.Fatal("surviving CHECK or latest staged descriptor lost", next.CheckConstraints)
	}
	integer, err := sqltype.Integer(next.Columns[0].Usage.Type, 11)
	if err != nil {
		t.Fatal(err)
	}
	result, err := next.CheckConstraints[0].Expression.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{integer, sqltype.String(next.Columns[1].Usage.Type, "0")}})
	if err != nil {
		t.Fatal("surviving CHECK slot migration", err)
	}
	valid, err := result.Boolean()
	if err != nil || !valid {
		t.Fatal(valid, err)
	}
	view := roleTableRef(t, e, "rr_stage_view")
	if _, removed := stage.Removals[view.Address]; !removed {
		t.Fatal("view removal missing")
	}
}

func TestRegclassDependencyIdentitySiblingTraversal(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, query := range []string{
		"CREATE TABLE rr_order_owner(v INT)",
		"CREATE TABLE rr_order_original(v INT)",
		"CREATE VIEW rr_order_view AS SELECT v FROM rr_order_original",
		"ALTER TABLE rr_order_owner ADD COLUMN r regclass DEFAULT 'rr_order_view' CONSTRAINT rr_order_zfirst CHECK(r='rr_order_view'::regclass) CONSTRAINT rr_order_afinal CHECK(r='rr_order_view'::regclass)",
		"CREATE VIEW rr_order_bridge AS SELECT v FROM rr_order_owner",
		"CREATE OR REPLACE VIEW rr_order_view AS SELECT v FROM rr_order_bridge",
	} {
		roleExecute(t, e, op, query)
	}
	ref := roleTableRef(t, e, "rr_order_owner")
	view := e.CatalogRead()
	descriptor, _ := view.Descriptor(ref.Address)
	owner := descriptor.(*catalog.Relation)
	target := ref.Address
	target.SubID = owner.Attributes[owner.Index["v"]]
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	views := viewDependencyClosure(view, []ids.ObjectAddress{target}, stage)
	expanded := []ids.ObjectAddress{target}
	for _, edge := range views {
		expanded = append(expanded, edge.view.Address())
	}
	checks, err := stageCheckDependents(stage, expanded, parser.DropRestrict)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := stageDefaultDependents(stage, expanded, parser.DropRestrict)
	if err != nil {
		t.Fatal(err)
	}
	ordered := supportedDependentLabels(view, []ids.ObjectAddress{target}, checks, views, defaults, nil)
	var labels []string
	for _, dependent := range ordered {
		if dependent.oid == 0 {
			t.Fatal("missing genuine consumer identity")
		}
		labels = append(labels, dependent.label)
	}
	want := []string{"view rr_order_bridge", "view rr_order_view", "default value for column r of table rr_order_owner", "constraint rr_order_zfirst on table rr_order_owner", "constraint rr_order_afinal on table rr_order_owner"}
	if !reflect.DeepEqual(labels, want) || ordered[0].oid <= ordered[1].oid || ordered[2].oid != owner.Defaults[owner.Attributes[owner.Index["r"]]].OID || ordered[3].oid != checks[0].check.OID || ordered[4].oid != checks[1].check.OID || ordered[3].oid <= ordered[4].oid {
		t.Fatal("provider traversal or genuine sibling identity lost", labels, ordered)
	}
	duplicate := supportedDependentLabels(view, []ids.ObjectAddress{target, target}, checks, views, defaults, nil)
	if !reflect.DeepEqual(duplicate, ordered) {
		t.Fatal("duplicate roots emitted consumers twice", duplicate)
	}
	other := target
	other.SubID = owner.Attributes[owner.Index["r"]]
	if got := supportedDependentLabels(view, []ids.ObjectAddress{other}, checks, views, defaults, nil); len(got) != 0 {
		t.Fatal("column identity ignored", got)
	}
}
