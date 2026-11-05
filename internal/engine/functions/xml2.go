package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// XML2Member is one released declaration in xml2--1.1.sql order.
type XML2Member struct {
	Name, Signature string
	Result          sqltype.ID
	Volatility      expr.Volatility
}

var XML2Members = [...]XML2Member{
	{"xml_valid", "25", sqltype.Bool, expr.Stable},
	{"xpath_string", "25,25", sqltype.Text, expr.Immutable},
	{"xpath_number", "25,25", sqltype.Float4, expr.Immutable},
	{"xpath_bool", "25,25", sqltype.Bool, expr.Immutable},
	{"xpath_table", "25,25,25,25,25", 2249, expr.Stable},
}

func XML2Lookup(name string) (XML2Member, bool) {
	for _, m := range XML2Members {
		if m.Name == name {
			return m, true
		}
	}
	return XML2Member{}, false
}

// XML2 retains only a released selector and immutable typed operands.
type XML2 struct {
	expr.Info
	Name string
	Args []expr.Expr
}

func (n XML2) Validate() error {
	m, ok := XML2Lookup(n.Name)
	count := 2
	if n.Name == "xml_valid" {
		count = 1
	}
	if !ok || n.Name == "xpath_table" || !n.Usage.Valid() || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != m.Result || len(n.Args) != count {
		return fmt.Errorf("invalid xml2 signature")
	}
	for _, a := range n.Args {
		if a == nil || !a.ResultUsage().Valid() || a.ResultUsage().Type.ID() != sqltype.Text {
			return fmt.Errorf("invalid xml2 operand")
		}
	}
	return nil
}
func (n XML2) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	null := false
	for i, arg := range n.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		null = null || values[i].IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	body, err := n.EnterRoutine(c, values[:len(n.Args)])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	input, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Name == "xml_valid" {
		return sqltype.XML2Valid(ctx, n.Usage.Type, input, c.FunctionInput())
	}
	path, err := values[1].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.XML2Scalar(ctx, n.Usage.Type, input, path)
}
func (n XML2) Children() []expr.Expr { return n.Args }
func (n XML2) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(n.Args) {
		return nil, fmt.Errorf("invalid xml2 children")
	}
	n.Info, n.Args = info, children
	return n, n.Validate()
}
func (n XML2) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n XML2) Retag(info expr.Info, children []expr.Expr, changed bool, _ expr.Retag) (expr.Expr, bool, error) {
	next, err := n.Rebuild(info, children)
	return next, changed, err
}
func (n XML2) IntrinsicVolatility() expr.Volatility {
	if n.Name == "xml_valid" {
		return expr.Stable
	}
	return expr.Immutable
}
func (n XML2) PlanningFold() expr.Fold {
	if n.Name == "xml_valid" {
		return expr.FoldNever
	}
	return expr.FoldConstant
}
func (n XML2) PreflightFold() expr.Preflight {
	if n.Name == "xml_valid" {
		return expr.PreflightChildren
	}
	return expr.PreflightSimplify
}
func (XML2) StrictNull() expr.Strict         { return expr.StrictAfter }
func (XML2) LooksLikeFunction() bool         { return true }
func (XML2) ClosureTransparent()             {}
func (XML2) SourceAdmission() (bool, bool)   { return true, false }
func (n XML2) ValidateStoredProvider() error { return n.Validate() }
func (n XML2) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	return expr.CaptureOperands{Args: n.Args, Count: len(n.Args)}, kind == expr.CaptureFunction, n.Validate()
}
func (n XML2) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(XML2)
	return ok && n.Name == o.Name && n.Usage == o.Usage && sameChildren(n.Args, o.Args, same)
}
func (n XML2) Render() (expr.Rendering, error) {
	args := make([]expr.RenderArg, len(n.Args))
	for i, a := range n.Args {
		args[i] = expr.RenderArg{Expr: a}
	}
	return expr.Rendering{Tree: "XML2", Form: expr.FormCall, Name: n.Name, Inputs: n.Args, Args: args}, n.Validate()
}
func (XML2) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.XML2") }
