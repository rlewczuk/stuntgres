package functions

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Rendering pins what each renderer reads from a node: the node-tree header
// and inputs, and validation of nodes that only a corrupted catalog produces.

func renderOf(t *testing.T, node expr.Renderable) expr.Rendering {
	t.Helper()
	spec, err := node.Render()
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestRenderTimezoneTreeKeepsWrittenOrder(t *testing.T) {
	d := temporalTestTypes()
	value := temporalTestLiteral(temporalTestTimestamp(t, d[sqltype.Timestamptz], 0))
	zone := temporalTestLiteral(sqltype.String(d[sqltype.Text], "UTC"))
	info := expr.Info{Usage: d[sqltype.Timestamp].Usage()}
	for _, c := range []struct {
		node   Timezone
		tree   string
		inputs []expr.Expr
	}{
		// The operator forms list the value first, unlike Children.
		{Timezone{Info: info, Value: value, Zone: zone}, "AtTimeZone", []expr.Expr{value, zone}},
		{Timezone{Info: info, Value: value, Local: true}, "AtLocal", []expr.Expr{value}},
		{Timezone{Info: info, Value: value, Zone: zone, Function: true}, "Datetime timezone", []expr.Expr{zone, value}},
	} {
		spec := renderOf(t, c.node)
		if spec.Tree != c.tree || !reflect.DeepEqual(spec.Inputs, c.inputs) {
			t.Errorf("%+v: got %q %v", c.node, spec.Tree, spec.Inputs)
		}
	}
}

func TestRenderBinaryTreeMarksTrimSyntax(t *testing.T) {
	types := formatUnitTypes()
	bytea := types[sqltype.Bytea]
	plan, err := sqltype.BindBinary("btrim", bytea.Usage(), bytea.Usage(), bytea.Usage())
	if err != nil {
		t.Fatal(err)
	}
	arg := binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage()}}
	node := Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: plan, Args: []expr.Expr{arg, arg}}
	if got := renderOf(t, node).Tree; got != "Binary btrim" {
		t.Errorf("ordinary: %q", got)
	}
	node.Syntax = parser.SQLFunctionSyntax
	if got := renderOf(t, node).Tree; got != "Binary btrim syntax TRIM" {
		t.Errorf("TRIM syntax: %q", got)
	}
	node.Args = node.Args[:1]
	if _, err := node.Render(); err == nil || err.Error() != "stuntgres: invalid stored binary syntax" {
		t.Errorf("one-argument TRIM syntax accepted: %v", err)
	}
}

func TestRenderArrayTransformTreeListsSlotsAndLabels(t *testing.T) {
	node := ArrayTransform{Kind: ArrayAppend, Namespace: "pg_catalog", Slots: [3]uint8{1, 0}, Labels: [3]string{"e", "a"}}
	want := `ArrayTransform array_append namespace pg_catalog slots [1 0 0] labels [e a ] operator false`
	if got := renderOf(t, node).Tree; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderLeafValidatesIntrinsicProperties(t *testing.T) {
	d := temporalTestTypes()
	node := TransactionTime{Info: expr.Info{Usage: d[sqltype.Timestamptz].Usage()}, Kind: TransactionTimeNow}
	node.Property = expr.IntrinsicProperties(node.Kind.IntrinsicRule())
	spec := renderOf(t, node)
	if spec.Tree != "TransactionTime kind 1" || spec.Inputs != nil {
		t.Errorf("got %q %v", spec.Tree, spec.Inputs)
	}
	node.Property = expr.Properties{}
	if _, err := node.Render(); err == nil {
		t.Error("leaf without its intrinsic properties rendered")
	}
}
