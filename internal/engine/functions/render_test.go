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

func TestRenderArrayTransformValidates(t *testing.T) {
	// Both renderers now share the deparser's validation of stored providers.
	node := ArrayTransform{Kind: ArrayAppend, Namespace: "pg_catalog", Slots: [3]uint8{1, 0}, Labels: [3]string{"e", "a"}}
	if _, err := node.Render(); err == nil || err.Error() != "stuntgres: invalid stored array transformation provider" {
		t.Errorf("unbound transformation rendered: %v", err)
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

func TestRenderBinaryForms(t *testing.T) {
	types := formatUnitTypes()
	bytea := types[sqltype.Bytea]
	arg := func() expr.Expr { return binaryUnitArgument{Info: expr.Info{Usage: bytea.Usage()}} }
	bind := func(name string) sqltype.BinaryPlan {
		plan, err := sqltype.BindBinary(name, bytea.Usage(), bytea.Usage(), bytea.Usage())
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	left, right := arg(), arg()
	concat := renderOf(t, Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: bind("||"), Args: []expr.Expr{left, right}})
	if concat.Form != expr.FormInfix || concat.Name != "||" || concat.Grouping != expr.GroupPrecedence ||
		!reflect.DeepEqual(concat.Args, []expr.RenderArg{{Expr: left, HideImplicit: true}, {Expr: right, HideImplicit: true}}) {
		t.Errorf("operator: %+v", concat)
	}
	trim := Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: bind("btrim"), Args: []expr.Expr{left, right}, Syntax: parser.SQLFunctionSyntax}
	spec := renderOf(t, trim)
	text, at := expr.TextPiece, expr.ArgPiece
	if spec.Form != expr.FormTemplate || !reflect.DeepEqual(spec.Pieces, []expr.Piece{text("TRIM(BOTH"), text(" "), at(1), text(" FROM "), at(0), text(")")}) || !spec.Args[0].HideImplicit {
		t.Errorf("TRIM syntax: %+v", spec)
	}
	trim.Syntax = parser.OrdinaryFunctionSyntax
	if spec := renderOf(t, trim); spec.Form != expr.FormCall || spec.Name != "btrim" || spec.Quote != expr.QuoteIdentifier || spec.Args[0].HideImplicit {
		t.Errorf("call: %+v", spec)
	}
}

func TestRenderStringSpecialSyntax(t *testing.T) {
	text := inspectCarrierTypes()[sqltype.Text]
	arg := func() expr.Expr { return stringUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text)} }
	node := func(name string, syntax parser.StringSyntax, args ...expr.Expr) String {
		result := text.Usage()
		switch name {
		case "is_normalized":
			result = inspectCarrierTypes()[sqltype.Bool].Usage()
		case "strpos":
			result = inspectCarrierTypes()[sqltype.Int4].Usage()
		}
		var textual [4]bool
		for i, arg := range args {
			textual[i] = arg.ResultUsage().Type == text
		}
		n, err := NewString(expr.Info{Usage: result}, name, text.Usage(), args, textual, syntax, false)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	word, at, value := expr.TextPiece, expr.ArgPiece, expr.ValuePiece
	for _, c := range []struct {
		node   String
		pieces []expr.Piece
	}{
		{node("normalize", parser.NormalizeStringSyntax, arg(), arg()), []expr.Piece{word("NORMALIZE("), at(0), word(", "), value(1), word(")")}},
		{node("normalize", parser.NormalizeStringSyntax, arg()), []expr.Piece{word("NORMALIZE("), at(0), word(")")}},
		{node("is_normalized", parser.NormalizedStringSyntax, arg(), arg()), []expr.Piece{word("("), at(0), word(" IS "), value(1), word(" "), word("NORMALIZED)")}},
		{node("btrim", parser.TrimStringSyntax, arg()), []expr.Piece{word("TRIM(BOTH"), word(" FROM "), at(0), word(")")}},
	} {
		spec := renderOf(t, c.node)
		if spec.Form != expr.FormTemplate || !reflect.DeepEqual(spec.Pieces, c.pieces) {
			t.Errorf("%s: %+v", c.node.Plan.Name(), spec.Pieces)
		}
	}
	if _, err := node("strpos", parser.PositionStringSyntax, arg(), arg()).Render(); err == nil || err.Error() != "stuntgres: invalid POSITION syntax provider" {
		t.Errorf("POSITION syntax over strpos: %v", err)
	}
	quoted := renderOf(t, node("left", parser.OrdinaryStringSyntax, arg(), stringUnitArgument{Info: expr.Info{Usage: inspectCarrierTypes()[sqltype.Int4].Usage()}}))
	if quoted.Form != expr.FormCall || quoted.Name != `"left"` || quoted.Quote != expr.QuoteVerbatim || !quoted.Args[0].Listed {
		t.Errorf("left: %+v", quoted)
	}
}

func TestRenderWrittenSlotsKeepLabels(t *testing.T) {
	d := temporalTestTypes()
	a := temporalTestLiteral(sqltype.String(d[sqltype.Text], "a"))
	b := temporalTestLiteral(sqltype.String(d[sqltype.Text], "b"))
	c := temporalTestLiteral(sqltype.String(d[sqltype.Text], "c"))
	node := JSONBPathExists{Args: [4]expr.Expr{a, b, c}, WrittenCount: 3, WrittenSlots: [4]uint8{1, 0, 2}, Labels: [4]string{"path", "target", ""}, Variadic: true}
	spec := renderOf(t, node)
	want := []expr.RenderArg{{Expr: b, Label: "path"}, {Expr: a, Label: "target"}, {Expr: c}}
	if spec.Name != "jsonb_path_exists" || spec.Quote != expr.QuoteVerbatim || spec.LabelQuote != expr.QuoteWhenNeeded || !spec.Variadic || !reflect.DeepEqual(spec.Args, want) {
		t.Errorf("got %+v", spec)
	}
	if spec.Tree != "JSONPath query" || !reflect.DeepEqual(spec.Inputs, []expr.Expr{a, b, c, nil}) {
		t.Errorf("tree %q %v", spec.Tree, spec.Inputs)
	}
	node.WrittenSlots = [4]uint8{1, 1, 2}
	if _, err := node.Render(); err == nil || err.Error() != "invalid stored JSONPath query argument" {
		t.Errorf("repeated slot: %v", err)
	}
	node.WrittenCount = 1
	if _, err := node.Render(); err == nil || err.Error() != "invalid stored JSONPath query" {
		t.Errorf("short call: %v", err)
	}
}

func TestRenderFormatOwnsUnknownTextFlags(t *testing.T) {
	d := temporalTestTypes()
	a := temporalTestLiteral(sqltype.String(d[sqltype.Text], "%s"))
	b := temporalTestLiteral(sqltype.String(d[sqltype.Text], "x"))
	spec := renderOf(t, Format{Args: []expr.Expr{a, b}, RawUnknown: [2]uint64{2}, Variadic: true})
	if spec.Name != "format" || spec.Quote != expr.QuoteIdentifier || !spec.UnknownTexts || !spec.Variadic ||
		spec.Args[0].UnknownText || !spec.Args[1].UnknownText || !spec.Args[0].Listed {
		t.Errorf("got %+v", spec)
	}
	if spec := renderOf(t, StringVariadic{Name: "concat", Args: []expr.Expr{a, b}}); spec.UnknownTexts {
		t.Errorf("concat replaced inherited unknown-text flags: %+v", spec)
	}
}
