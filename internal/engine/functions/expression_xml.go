package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// XMLParse consumes the analyzer's ordinary TEXT assignment conversion.
// Provider events belong to execution, never this immutable recipe.
type XMLParse struct {
	expr.Info
	Child      expr.Expr
	Document   bool
	Whitespace string
	Syntax     parser.XMLParseExpr
}

func xmlMode(document bool) sqltype.XMLMode {
	if document {
		return sqltype.XMLDocument
	}
	return sqltype.XMLContent
}

func (e XMLParse) Eval(c expr.Context) (sqltype.Value, error) {
	value, err := e.Child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	text, err := value.Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.XMLInput(ctx, e.Usage.Type, text, xmlMode(e.Document), c.FunctionInput())
}

func (e XMLParse) Children() []expr.Expr { return []expr.Expr{e.Child} }

func (e XMLParse) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	if len(children) != 1 || children[0] == nil || children[0].ResultUsage().Type == nil || children[0].ResultUsage().Type.ID() != sqltype.Text || info.Usage.Type == nil || info.Usage.Type.ID() != sqltype.XML {
		return nil, fmt.Errorf("invalid XMLPARSE recipe")
	}
	e.Info, e.Child = info, children[0]
	return e, nil
}

func (e XMLParse) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e XMLParse) PlanningFold() expr.Fold              { return expr.FoldNever }
func (e XMLParse) ExecutionDemand()                     {}
func (e XMLParse) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (e XMLParse) StrictNull() expr.Strict              { return expr.StrictAfter }
func (e XMLParse) LooksLikeFunction() bool              { return true }
func (e XMLParse) ClosureTransparent()                  {}

func (e XMLParse) ValidateStoredProvider() error {
	if e.Whitespace != "" && e.Whitespace != "preserve" && e.Whitespace != "strip" {
		return fmt.Errorf("invalid XMLPARSE whitespace option")
	}
	if e.Child == nil || e.Child.ResultUsage().Type == nil || e.Child.ResultUsage().Type.ID() != sqltype.Text || e.Usage.Type == nil || e.Usage.Type.ID() != sqltype.XML {
		return fmt.Errorf("invalid XMLPARSE recipe")
	}
	return nil
}

func (e XMLParse) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{e.Child}, XMLSources: true}, kind == expr.CaptureXMLParse, nil
}

func xmlModeSQL(document bool) string {
	if document {
		return "DOCUMENT"
	}
	return "CONTENT"
}

func (e XMLParse) Render() (expr.Rendering, error) {
	mode, suffix := xmlModeSQL(e.Document), ")"
	if e.Whitespace != "" {
		suffix = " " + strings.ToUpper(e.Whitespace) + " WHITESPACE)"
	}
	return expr.Rendering{
		Tree: "XMLPARSE " + mode, Inputs: e.Children(), Form: expr.FormTemplate,
		Args:   []expr.RenderArg{{Expr: e.Child, HideImplicit: true, XMLSource: e.Pos}},
		Pieces: []expr.Piece{expr.TextPiece("XMLPARSE(" + mode + " "), expr.ArgPiece(0), expr.TextPiece(suffix)},
	}, e.ValidateStoredProvider()
}

func (e XMLParse) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(XMLParse)
	return ok && e.Document == o.Document && e.Whitespace == o.Whitespace && same(e.Child, o.Child)
}

// XMLSerialize produces strict TEXT characters. The analyzer owns its enclosing
// ordinary implicit target conversion, including nonstrict catalog cast routines.
type XMLSerialize struct {
	expr.Info
	Child    expr.Expr
	Document bool
	Indent   string
	Syntax   parser.XMLSerializeExpr
}

func NewXMLSerialize(info expr.Info, child expr.Expr, document bool, indent string) (XMLSerialize, error) {
	if indent != "" && indent != "indent" && indent != "no indent" {
		return XMLSerialize{}, fmt.Errorf("invalid XMLSERIALIZE indent option")
	}
	if info.Usage.Type == nil || info.Usage.Type.ID() != sqltype.Text || child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != sqltype.XML {
		return XMLSerialize{}, fmt.Errorf("invalid XMLSERIALIZE recipe")
	}
	return XMLSerialize{Info: info, Child: child, Document: document, Indent: indent}, nil
}

func (e XMLSerialize) Eval(c expr.Context) (sqltype.Value, error) {
	value, err := e.Child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	text, err := sqltype.XMLSerialize(ctx, value, xmlMode(e.Document), e.Indent == "indent", c.FunctionInput())
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.String(e.Usage.Type, text), nil
}

func (e XMLSerialize) Children() []expr.Expr { return []expr.Expr{e.Child} }

func (e XMLSerialize) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := e.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	if len(children) != 1 {
		return nil, fmt.Errorf("invalid XMLSERIALIZE children")
	}
	next, err := NewXMLSerialize(info, children[0], e.Document, e.Indent)
	next.Syntax = e.Syntax
	return next, err
}

func (e XMLSerialize) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e XMLSerialize) PlanningFold() expr.Fold              { return expr.FoldNever }
func (e XMLSerialize) ExecutionDemand()                     {}
func (e XMLSerialize) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (e XMLSerialize) LooksLikeFunction() bool              { return true }
func (e XMLSerialize) ClosureTransparent()                  {}

func (e XMLSerialize) StrictNull() expr.Strict { return expr.StrictAfter }

func (e XMLSerialize) ValidateStoredProvider() error {
	_, err := NewXMLSerialize(e.Info, e.Child, e.Document, e.Indent)
	return err
}

func (e XMLSerialize) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Count: 1, Inline: [2]expr.Expr{e.Child}, XMLSources: true}, kind == expr.CaptureXMLSerialize, nil
}

func (e XMLSerialize) Render() (expr.Rendering, error) {
	mode, suffix := xmlModeSQL(e.Document), ")"
	if e.Indent != "" {
		suffix = " " + strings.ToUpper(e.Indent) + ")"
	}
	return expr.Rendering{
		Tree: "XMLSERIALIZE " + mode, Inputs: e.Children(), Form: expr.FormTemplate,
		Args: []expr.RenderArg{{Expr: e.Child, HideImplicit: true, XMLSource: e.Pos}}, OwnedReturning: e.Usage,
		Pieces: []expr.Piece{expr.TextPiece("XMLSERIALIZE(" + mode + " "), expr.ArgPiece(0), {Returning: true, Text: " AS ", Suffix: suffix}},
	}, e.ValidateStoredProvider()
}

func (e XMLSerialize) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(XMLSerialize)
	return ok && e.Document == o.Document && e.Indent == o.Indent && e.Usage == o.Usage && same(e.Child, o.Child)
}

func BindXMLParse(b Binder, raw parser.XMLParseExpr, source expr.Expr) (expr.Expr, error) {
	node := XMLParse{Info: b.Info(b.Type(sqltype.XML).Usage(), raw.Pos, source.Properties()), Child: source, Document: raw.Document, Whitespace: raw.Whitespace, Syntax: raw}
	return node, node.ValidateStoredProvider()
}

// BindXMLSerialize binds the strict character provider before resolving the
// target. The caller applies the returned target through its ordinary coercer.
func BindXMLSerialize(b Binder, raw parser.XMLSerializeExpr, child expr.Expr) (expr.Expr, sqltype.Usage, error) {
	target, err := b.JSONReturningType(raw.Target)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	text := b.Type(sqltype.Text).Usage()
	if !b.CanCoerce(text, target, sqltype.Implicit) {
		return nil, sqltype.Usage{}, expr.ErrorAt("42846", "cannot cast XMLSERIALIZE result to "+b.DiagnosticTypeName(target.Type), raw.Pos)
	}
	next, err := NewXMLSerialize(b.Info(text, raw.Pos, child.Properties()), child, raw.Document, raw.Indent)
	next.Syntax = raw
	return next, target, err
}
