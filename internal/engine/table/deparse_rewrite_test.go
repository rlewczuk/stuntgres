package table

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDeparseRewriteReturnInventory(t *testing.T) {
	f := newDeparseFixture(t)
	state := catalog.CloneState(f.in.Catalog.State())
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}
	boolean := builtinType(t, sqltype.Bool)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	raw := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true}}
	syntax := &parser.Select{Targets: []parser.Expr{raw}}
	project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "flag", Usage: boolean.Usage()}}, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{literal}, Syntax: syntax, Frame: 1}
	view := stored.NewView(stored.ViewParts{Rule: 201, Query: &plan.Query{Root: project}, Syntax: syntax, Frames: map[uint32]*plan.ProjectNode{1: project}, Queries: map[*parser.Select]*plan.ProjectNode{syntax: project}, Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{{Query: 1, Clause: analyze.ViewTargetLiteral}: literal}})
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 200}, ObjectNamespace: namespace, ObjectName: "v", RelationKind: catalog.ViewRelation, Columns: []catalog.Column{{Name: "flag", Usage: boolean.Usage()}}, Attributes: []int32{1}, View: view}
	bootstrap := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 202}, ObjectNamespace: namespace, ObjectName: "bootstrap", RelationKind: catalog.CatalogRelation, CatalogView: true, CatalogReturnRule: 203}
	for _, d := range []*catalog.Relation{relation, bootstrap} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	f.in.Catalog = f.in.Catalog.WithState(state)
	f.in.Path = []ids.ObjectAddress{namespace}
	f.in.BuiltinViewDefinition = func(ctx context.Context, read catalog.Read, d *catalog.Relation, pretty bool) (string, error) {
		if read.State() != state || d != bootstrap {
			t.Fatal("callback lost captured authority")
		}
		if pretty {
			return " SELECT 2 AS flag", ctx.Err()
		}
		return " SELECT (2) AS flag", ctx.Err()
	}
	before := view.Parts()
	for _, tc := range []struct {
		oid    uint32
		pretty bool
		want   string
	}{{201, false, "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO public.v DO INSTEAD  SELECT true AS flag;"}, {201, true, "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO v DO INSTEAD  SELECT true AS flag;"}, {203, false, "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO public.bootstrap DO INSTEAD  SELECT (2) AS flag;"}, {203, true, "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO bootstrap DO INSTEAD  SELECT 2 AS flag;"}} {
		oid, err := sqltype.OIDValue(builtinType(t, sqltype.Oid), tc.oid)
		if err != nil {
			t.Fatal(err)
		}
		c := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
		value, err := Deparse(c, f.in, functions.DeparseRulePretty, [3]sqltype.Value{oid, sqltype.Boolean(boolean, tc.pretty)}, f.text)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := value.Text(); got != tc.want {
			t.Fatalf("definition %q; want %q", got, tc.want)
		}
		after := view.Parts()
		if !reflect.DeepEqual(before, after) || view.Query() != before.Query || view.Syntax() != syntax || view.Query().Root != project || &before.Query.Root.(*plan.ProjectNode).Expressions[0] != &project.Expressions[0] || relation.View != view {
			t.Fatal("return-rule presentation replaced frozen view backing")
		}
	}
	oid, err := sqltype.OIDValue(builtinType(t, sqltype.Oid), 203)
	if err != nil {
		t.Fatal(err)
	}
	f.in.BuiltinViewDefinition = nil
	c := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
	if value, err := Deparse(c, f.in, functions.DeparseRule, [3]sqltype.Value{oid}, f.text); err == nil || value.Type() != nil {
		t.Fatal("advertised missing authority became NULL", value, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	c.Context = ctx
	if value, err := Deparse(c, f.in, functions.DeparseRule, [3]sqltype.Value{oid}, f.text); !errors.Is(err, context.Canceled) || value.Type() != nil {
		t.Fatal("canceled inquiry published result", value, err)
	}
}

func TestDeparseRewriteTemporaryNaming(t *testing.T) {
	f := newDeparseFixture(t)
	state := catalog.CloneState(f.in.Catalog.State())
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 400}
	owner := f.in.Catalog.Owner().Domain.Session(1)
	state.SetDescriptor(namespace, &catalog.Namespace{ObjectAddress: namespace, ObjectName: "pg_temp_42", Temporary: owner})
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 401}, ObjectNamespace: namespace, ObjectName: "v", RelationKind: catalog.CatalogRelation, CatalogView: true, CatalogReturnRule: 402}
	state.SetDescriptor(relation.Address(), relation)
	state.BindName(catalog.DescriptorNameKey(relation), relation.Address())
	f.in.Catalog = f.in.Catalog.WithState(state)
	f.in.BuiltinViewDefinition = func(context.Context, catalog.Read, *catalog.Relation, bool) (string, error) {
		return " SELECT true AS flag", nil
	}
	// The fixture's Temporary callback fails if called: naming consumes only
	// the already captured token and namespace identity, never lazy creation.
	oid, err := sqltype.OIDValue(builtinType(t, sqltype.Oid), 402)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token   ids.SessionToken
		visible bool
		want    string
	}{{owner, false, "pg_temp.v"}, {owner, true, "v"}, {f.in.Catalog.Owner().Domain.Session(2), false, "pg_temp_42.v"}, {ids.SessionToken{}, false, "pg_temp_42.v"}} {
		f.in.TemporaryOwner = tc.token
		f.in.Path = nil
		if tc.visible {
			f.in.Path = []ids.ObjectAddress{namespace}
		}
		c := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
		value, err := Deparse(c, f.in, functions.DeparseRulePretty, [3]sqltype.Value{oid, sqltype.Boolean(builtinType(t, sqltype.Bool), tc.visible)}, f.text)
		if err != nil {
			t.Fatal(err)
		}
		want := "CREATE RULE \"_RETURN\" AS\n    ON SELECT TO " + tc.want + " DO INSTEAD  SELECT true AS flag;"
		if text, _ := value.Text(); text != want {
			t.Fatal(text, want)
		}
	}
}
