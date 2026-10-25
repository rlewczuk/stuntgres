package engine

import (
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func ruleUnitCatalog(t *testing.T) (*core.Engine, core.AuthorizationView, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
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

func ruleUnitRef(e *core.Engine, d *catalog.Relation) ids.ObjectRef {
	return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
}

func ruleUnitInt(d *catalog.Relation, n int64) expr.Expr {
	value, err := sqltype.Integer(d.Columns[0].Usage.Type, n)
	if err != nil {
		panic(err)
	}
	return plan.LiteralExpr{Info: expr.Info{Usage: d.Columns[0].Usage, Pos: parser.Position{Offset: -1}}, Value: value}
}

func ruleUnitInsert(e *core.Engine, d *catalog.Relation, id, v int64) stored.RuleAction {
	binding := analyze.Binding{Relation: d, Ref: ruleUnitRef(e, d), Targets: []int{0, 1}, Expressions: [][]expr.Expr{{ruleUnitInt(d, id), ruleUnitInt(d, v)}}}
	presentation := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: plan.QuerySchema(d.Columns), Child: &plan.SingletonNode{}, Expressions: binding.Expressions[0]}}})
	return stored.NewRuleAction(stored.RuleActionParts{Syntax: &parser.Insert{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: d.Name()}}}}, Binding: binding, Presentation: presentation})
}
