package core_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRuleConflictInstantiation(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	original := ruleUnitInsert(e, src, 1, 10).Binding()
	action := ruleUnitInsert(e, sink, 1, 10)
	recipe := &analyze.InsertConflict{Targets: action.Binding().Targets, Keys: []analyze.ConflictKey{{Attribute: 1}}, Update: &analyze.UpdateBinding{Assignments: []expr.Expr{ruleUnitInt(sink, 20)}}, UpdateTargets: []int{1}, Width: 2}
	action = action.With(func(p *stored.RuleActionParts) { p.Binding.Insert.Conflict = recipe })
	first, err := view.ComposeRuleAction(auth, original, "INSERT", stored.Rule{}, action)
	if err != nil {
		t.Fatal(err)
	}
	// Execution replaces the command's normalized predicate through its state.
	state := dml.NewInsertConflictState(first.Insert.Conflict)
	state.Predicate = ruleUnitInt(sink, 30)
	state.MarkAffected(ids.RowID(42), 0)
	if first.Insert.Conflict.Predicate == nil {
		t.Fatal("conflict state does not execute the command's clause")
	}
	second, err := view.ComposeRuleAction(auth, original, "INSERT", stored.Rule{}, action)
	if err != nil {
		t.Fatal(err)
	}
	if first.Insert.Conflict == second.Insert.Conflict || second.Insert.Conflict == recipe || first.Insert.Conflict == recipe {
		t.Fatal("composed commands share conflict workspace")
	}
	for _, c := range []*analyze.InsertConflict{recipe, second.Insert.Conflict} {
		if c.Predicate != nil {
			t.Fatal("execution state escaped its composed command")
		}
	}
	if !slices.Equal(second.Insert.Conflict.UpdateTargets, []int{1}) {
		t.Fatal("instantiation changed analyzed recipe")
	}
}

func TestRuleConflictAttributeRefresh(t *testing.T) {
	e, auth, _, sink := ruleUnitCatalog(t)
	b := ruleUnitInsert(e, sink, 1, 10).Binding()
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

func TestRuleIndependentActionProducer(t *testing.T) {
	e, auth, src, sink := ruleUnitCatalog(t)
	for _, event := range []string{"UPDATE", "DELETE"} {
		t.Run(event, func(t *testing.T) {
			original := ruleUnitInsert(e, src, 1, 10).Binding()
			if event == "UPDATE" {
				original.Update = &analyze.UpdateBinding{Assignments: []expr.Expr{ruleUnitInt(src, 1), ruleUnitInt(src, 10)}}
			}
			action := ruleUnitInsert(e, sink, 7, 8)
			composed, err := view.ComposeRuleAction(auth, original, event, stored.Rule{}, action)
			if err != nil {
				t.Fatal(err)
			}
			join := composed.Query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
			if _, ok := join.Left.(*plan.SingletonNode); !ok || len(composed.QuerySources) != 0 {
				t.Fatal("independent action still depends on event rows")
			}
			ref := original.Ref
			ref.Address.SubID = src.Attributes[0]
			action = action.With(func(p *stored.RuleActionParts) {
				p.Binding.Expressions = [][]expr.Expr{{plan.ColumnExpr{Info: view.RuleColumnInfo(src.Columns[0]), Origin: ref, Scope: analyze.RulePseudoScope, Frame: 1}, ruleUnitInt(sink, 8)}}
			})
			dependent, err := view.ComposeRuleAction(auth, original, event, stored.Rule{}, action)
			if err != nil {
				t.Fatal(err)
			}
			if len(dependent.QuerySources) == 0 {
				t.Fatal("OLD demand lost event-row authority")
			}
			if action.Binding().Expressions[0][0].(plan.ColumnExpr).Scope != analyze.RulePseudoScope {
				t.Fatal("composition mutated stored action")
			}
		})
	}
}

func ruleUnitCatalog(t *testing.T) (*engine.Engine, engine.AuthorizationView, *catalog.Relation, *catalog.Relation) {
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

func ruleUnitInsert(e *engine.Engine, d *catalog.Relation, id, v int64) stored.RuleAction {
	binding := analyze.Binding{Relation: d, Ref: ruleUnitRef(e, d), Targets: []int{0, 1}, Expressions: [][]expr.Expr{{ruleUnitInt(d, id), ruleUnitInt(d, v)}}}
	presentation := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(d.Columns), Child: &plan.SingletonNode{}, Expressions: binding.Expressions[0]}}})
	return stored.NewRuleAction(stored.RuleActionParts{Syntax: &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}}, Binding: binding, Presentation: presentation})
}

func ruleUnitInt(d *catalog.Relation, n int64) expr.Expr {
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, n)
	if err != nil {
		panic(err)
	}
	return plan.LiteralExpr{Info: expr.Info{Usage: d.Columns[0].Usage, Pos: parser.Position{Offset: -1}}, Value: value}
}

func ruleUnitRef(e *engine.Engine, d *catalog.Relation) ids.ObjectRef {
	return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
}
