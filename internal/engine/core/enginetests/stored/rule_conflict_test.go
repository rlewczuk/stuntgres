package stored_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
)

func TestRuleConflictAttributeRefresh(t *testing.T) {
	e, auth, _, sink := ruleUnitCatalog(t)
	b := enginetest.RuleUnitInsert(e, sink, 1, 10).Binding()
	ref := b.Ref
	ref.Address.SubID = sink.Attributes[1]
	target := plan.ColumnExpr{Info: view.RuleColumnInfo(sink.Columns[1]), Origin: ref, Scope: 1, Frame: 1, Slot: 1}
	excluded := target
	excluded.Slot = 3
	b.Insert.Conflict = &analyze.InsertConflict{Targets: b.Targets, Reads: []int{0}, ActionReads: []int{1}, UpdateTargets: []int{1}, Width: 2, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{excluded}, Predicate: target}}
	current := *sink
	current.Attributes = []int32{2, 1}
	current.Columns = []catalog.Column{sink.Columns[1], sink.Columns[0]}
	current.Index = map[string]int{"v": 0, "id": 1}
	catalog := catalog.CloneState(auth.Catalog.State())
	catalog.SetDescriptor(sink.Address(), &current)
	view := e.Catalog.Read(catalog)
	check := func(next analyze.Binding) {
		t.Helper()
		if !slices.Equal(next.Targets, []int{1, 0}) || !slices.Equal(next.Insert.Conflict.Targets, []int{1, 0}) || !slices.Equal(next.Insert.Conflict.UpdateTargets, []int{0}) || !slices.Equal(next.Insert.Conflict.Reads, []int{1}) || !slices.Equal(next.Insert.Conflict.ActionReads, []int{0}) {
			t.Fatal("conflict manifests did not follow stable attributes")
		}
		if next.Insert.Conflict.Update.Assignments[0].(plan.ColumnExpr).Slot != 2 || next.Insert.Conflict.Update.Predicate.(plan.ColumnExpr).Slot != 0 || next.Insert.Conflict.Width != 2 {
			t.Fatal("target/EXCLUDED namespaces lost their distinct halves")
		}
	}
	next, err := stored.RefreshRuleBinding(view, b, &current)
	if err != nil {
		t.Fatal(err)
	}
	check(next)
	mapped, err := stored.RemapRuleBinding(view, b, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	check(mapped)
	if b.Targets[0] != 0 || b.Insert.Conflict.UpdateTargets[0] != 1 || b.Insert.Conflict.Update.Assignments[0].(plan.ColumnExpr).Slot != 3 || b.Relation != sink {
		t.Fatal("refresh/remap mutated the stored template")
	}
}

func ruleUnitCatalog(t *testing.T) (*core.Engine, core.AuthorizationView, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	base := e.CatalogRead()
	typ, ok := base.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("INT4 unavailable")
	}
	makeRelation := func(id ids.OID, name string) *catalog.Relation {
		return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: name, ObjectRevision: 1, Columns: []catalog.Column{{Name: "id", Usage: typ.Usage()}, {Name: "v", Usage: typ.Usage()}}, Index: map[string]int{"id": 0, "v": 1}, Attributes: []int32{1, 2}, ColumnMeta: catalog.ColumnMetadata{HighWater: 2}, RelationKind: catalog.TableRelation}
	}
	src, sink := makeRelation(900001, "src"), makeRelation(900002, "sink")
	next := catalog.CloneState(e.Catalog.State)
	next.SetDescriptor(src.Address(), src)
	next.SetDescriptor(sink.Address(), sink)
	e.Catalog.PublishLocked(next)
	return e, e.AuthorityView(access.View{Owner: &e.Catalog.Owner, Catalog: e.Catalog.Read(next)}), src, sink
}
