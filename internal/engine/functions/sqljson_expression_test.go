package functions

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The SQL/JSON constructor and IS JSON nodes take part in the engine switches
// through the node protocol: children, rebuild, intrinsic volatility, stored
// generation admission and their node-tree and SQL presentation.

func TestSQLJSONProtocol(t *testing.T) {
	d := temporalTestTypes()
	first := temporalTestLiteral(sqltype.String(d[sqltype.Text], "a"))
	second := temporalTestLiteral(sqltype.String(d[sqltype.Text], "b"))
	raw := parser.SQLJSONExpr{Kind: "json_array", Absent: true}
	info := expr.Info{Usage: d[sqltype.JSON].Usage(), Property: expr.Properties{Volatility: expr.Stable}}
	node, err := NewSQLJSON(info, raw, []expr.Expr{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if got := node.Children(); !reflect.DeepEqual(got, []expr.Expr{first, second}) {
		t.Fatalf("children = %v", got)
	}
	rebuilt, err := node.Rebuild(info, []expr.Expr{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if got := rebuilt.(SQLJSON); !reflect.DeepEqual(got.Args, []expr.Expr{second, first}) || got.Raw.Kind != raw.Kind || !reflect.DeepEqual(node.Args, []expr.Expr{first, second}) {
		t.Fatalf("rebuilt %#v from %#v", got, node)
	}
	if _, err := node.Rebuild(info, []expr.Expr{first}); err == nil {
		t.Fatal("rebuild accepted a missing child")
	}
	if _, err := node.Rebuild(expr.Info{Usage: d[sqltype.Text].Usage()}, []expr.Expr{first, second}); err == nil {
		t.Fatal("rebuild did not revalidate the constructor plan")
	}
	// Binding merged the children's volatility into the node's own.
	if got := node.IntrinsicVolatility(); got != expr.Stable {
		t.Fatalf("intrinsic volatility = %v", got)
	}
	if err := node.ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
	spec := renderOf(t, node)
	if spec.Tree != "SQLJSON json_array absent true unique false" || !reflect.DeepEqual(spec.Inputs, []expr.Expr{first, second}) || spec.Form != expr.FormNone {
		t.Fatalf("rendering = %#v", spec)
	}
}

func TestJSONPredicateProtocol(t *testing.T) {
	d := temporalTestTypes()
	child := temporalTestLiteral(sqltype.String(d[sqltype.Text], "{}"))
	other := temporalTestLiteral(sqltype.String(d[sqltype.Text], "[]"))
	info := expr.Info{Usage: d[sqltype.Bool].Usage()}
	node := JSONPredicate{Info: info, Child: child, Kind: "object", Unique: true, Not: true}
	rebuildIdentity(t, node, []expr.Expr{child})
	rebuilt, err := node.Rebuild(info, []expr.Expr{other})
	if err != nil || rebuilt.(JSONPredicate).Child != other || node.Child != child {
		t.Fatalf("rebuilt %#v, %v", rebuilt, err)
	}
	if got := node.IntrinsicVolatility(); got != expr.Immutable {
		t.Fatalf("intrinsic volatility = %v", got)
	}
	if err := node.ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		node JSONPredicate
		tree string
		tail string
	}{
		{node, "IS JSON object unique true not true", " IS NOT JSON OBJECT WITH UNIQUE KEYS)"},
		{JSONPredicate{Info: info, Child: child}, "IS JSON  unique false not false", " IS JSON)"},
		{JSONPredicate{Info: info, Child: child, Kind: "scalar"}, "IS JSON scalar unique false not false", " IS JSON SCALAR)"},
	} {
		spec := renderOf(t, c.node)
		want := []expr.Piece{expr.TextPiece("("), expr.ArgPiece(0), expr.TextPiece(c.tail)}
		if spec.Tree != c.tree || !reflect.DeepEqual(spec.Inputs, []expr.Expr{child}) || spec.Form != expr.FormTemplate ||
			!reflect.DeepEqual(spec.Pieces, want) || !reflect.DeepEqual(spec.Args, []expr.RenderArg{{Expr: child, HideImplicit: true}}) {
			t.Fatalf("rendering = %#v", spec)
		}
	}
}
