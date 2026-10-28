package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type xmlUnitNotices struct{ warnings []sqlstate.Error }

func (n *xmlUnitNotices) XMLInputDiagnostic(e sqlstate.Error) sqlstate.Error { return e }
func (n *xmlUnitNotices) XMLInputWarning(e sqlstate.Error)                   { n.warnings = append(n.warnings, e) }

func TestXMLExpressionProtocol(t *testing.T) {
	types := temporalTestTypes()
	text := types[sqltype.Text].Usage()
	xml := types[sqltype.XML].Usage()
	argument := temporalTestLiteral(sqltype.String(text.Type, `<a><b/></a>`))
	parse := XMLParse{Info: expr.Info{Usage: xml}, Child: argument, Document: true, Whitespace: "preserve"}
	ctx := &temporalTestContext{Context: context.Background()}
	value, err := parse.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original, err := value.XML()
	if err != nil || original.Original() != `<a><b/></a>` {
		t.Fatal(original, err)
	}
	if parse.PlanningFold() != expr.FoldNever || parse.StrictNull() != expr.StrictAfter {
		t.Fatal("moved XMLPARSE timing")
	}
	if _, err := parse.Rebuild(parse.Info, nil); err == nil {
		t.Fatal("missing child admitted")
	}
	capture, ok, err := parse.Capture(expr.CaptureXMLParse)
	if err != nil || !ok || capture.Check(parse, 1) != nil {
		t.Fatal(capture, ok, err)
	}
	if _, ok, _ := parse.Capture(expr.CaptureXMLSerialize); ok {
		t.Fatal("parse admitted serializer syntax")
	}
	if spec, err := parse.Render(); err != nil || spec.Pieces[0].Text != "XMLPARSE(DOCUMENT " || spec.Pieces[2].Text != " PRESERVE WHITESPACE)" {
		t.Fatal(spec, err)
	}
	serialize, err := NewXMLSerialize(expr.Info{Usage: text}, temporalTestLiteral(value), true, "indent")
	if err != nil {
		t.Fatal(err)
	}
	got, err := serialize.Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	chars, _ := got.Text()
	if chars != "<a>\n  <b/>\n</a>" {
		t.Fatal(chars)
	}
	if serialize.PlanningFold() != expr.FoldNever || serialize.ValidateStoredProvider() != nil {
		t.Fatal("invalid serializer protocol")
	}
	if _, err := serialize.Rebuild(serialize.Info, nil); err == nil {
		t.Fatal("missing serialize child admitted")
	}
	if _, err := serialize.Rebuild(serialize.Info, []expr.Expr{argument}); err == nil {
		t.Fatal("concrete text child admitted")
	}
	if _, err := parse.Rebuild(parse.Info, []expr.Expr{temporalTestLiteral(sqltype.Boolean(types[sqltype.Bool], true))}); err == nil {
		t.Fatal("non-TEXT producing source admitted")
	}
	rebuilt, err := parse.Rebuild(parse.Info, parse.Children())
	if err != nil || rebuilt.(XMLParse).Child != parse.Child {
		t.Fatal("identity rebuild changed source backing", err)
	}
	if !sqltype.SameDatum(value, serialize.Child.(temporalTestArgument).value) {
		t.Fatal("recipe replaced immutable XML backing")
	}
}

func TestXMLExpressionCallDiagnostics(t *testing.T) {
	types := temporalTestTypes()
	text := types[sqltype.Text].Usage()
	xml := types[sqltype.XML].Usage()
	notices := &xmlUnitNotices{}
	ctx := &temporalTestContext{Context: context.Background(), input: sqltype.InputEnvironment{XMLInput: notices}}
	parse := XMLParse{Info: expr.Info{Usage: xml}, Child: temporalTestLiteral(sqltype.String(text.Type, `<?xml version="1.1"?><a/>`)), Document: true}
	value, err := parse.Eval(ctx)
	if err != nil || len(notices.warnings) != 1 {
		t.Fatal(value, len(notices.warnings), err)
	}
	serialize, err := NewXMLSerialize(expr.Info{Usage: text}, temporalTestLiteral(value), true, "indent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = serialize.Eval(ctx); err != nil || len(notices.warnings) != 2 {
		t.Fatal("composed per-call warnings", len(notices.warnings), err)
	}
	if notices.warnings[0].Message != notices.warnings[1].Message || notices.warnings[0].Code != "01000" {
		t.Fatal(notices.warnings)
	}
	serialize.Indent = "no indent"
	if _, err = serialize.Eval(ctx); err != nil || len(notices.warnings) != 3 || notices.warnings[2].Message != notices.warnings[0].Message {
		t.Fatal("DOCUMENT qualification warning", notices.warnings, err)
	}
	serialize.Document = false
	if _, err = serialize.Eval(ctx); err != nil || len(notices.warnings) != 3 {
		t.Fatal("old warnings replayed", err)
	}
	parse.Child = temporalTestLiteral(sqltype.Null(text.Type))
	if v, err := parse.Eval(ctx); err != nil || !v.IsNull() || v.Type() != xml.Type || len(notices.warnings) != 3 {
		t.Fatal(v, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	ctx.Context = canceled
	parse.Child = temporalTestLiteral(sqltype.String(text.Type, `<a/>`))
	if _, err := parse.Eval(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
