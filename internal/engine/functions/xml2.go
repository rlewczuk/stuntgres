package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type XML2Operation uint8

const (
	XML2Valid XML2Operation = iota
	XML2String
	XML2Number
	XML2Boolean
	XML2Nodeset
	XML2List
	XML2Table
	XML2XSLT
)

// XML2Member names an exact released signature. Kernel is the authentic full
// arity installation slot for a structural SQL wrapper, or -1 for a kernel.
type XML2Member struct {
	Name, Signature string
	Result          sqltype.ID
	Volatility      expr.Volatility
	Arity           int
	Operation       XML2Operation
	Kernel          int
}

var XML2Members = [...]XML2Member{
	{"xml_valid", "25", sqltype.Bool, expr.Stable, 1, XML2Valid, -1},
	{"xpath_string", "25,25", sqltype.Text, expr.Immutable, 2, XML2String, -1},
	{"xpath_number", "25,25", sqltype.Float4, expr.Immutable, 2, XML2Number, -1},
	{"xpath_bool", "25,25", sqltype.Bool, expr.Immutable, 2, XML2Boolean, -1},
	{"xpath_table", "25,25,25,25,25", 2249, expr.Stable, 5, XML2Table, -1},
	{"xpath_nodeset", "25,25,25,25", sqltype.Text, expr.Immutable, 4, XML2Nodeset, -1},
	{"xpath_nodeset", "25,25,25", sqltype.Text, expr.Immutable, 3, XML2Nodeset, 5},
	{"xpath_nodeset", "25,25", sqltype.Text, expr.Immutable, 2, XML2Nodeset, 5},
	{"xpath_list", "25,25,25", sqltype.Text, expr.Immutable, 3, XML2List, -1},
	{"xpath_list", "25,25", sqltype.Text, expr.Immutable, 2, XML2List, 8},
	{"xslt_process", "25,25,25", sqltype.Text, expr.Volatile, 3, XML2XSLT, -1},
	{"xslt_process", "25,25", sqltype.Text, expr.Immutable, 2, XML2XSLT, -1},
}

func XML2Lookup(name string, arity int) (XML2Member, bool) {
	for _, m := range XML2Members {
		if m.Name == name && m.Arity == arity {
			return m, true
		}
	}
	return XML2Member{}, false
}

// XML2 retains only a released selector and immutable typed operands.
type XML2 struct {
	expr.Info
	Name      string
	Args      []expr.Expr
	Operation XML2Operation
}

func (n XML2) Validate() error {
	m, ok := XML2Lookup(n.Name, len(n.Args))
	if !ok || n.Operation != m.Operation || n.Operation == XML2Table || !n.Usage.Valid() || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != m.Result {
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
	_, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	member, _ := XML2Lookup(n.Name, len(n.Args))
	if member.Kernel >= 0 {
		return sqltype.Value{}, fmt.Errorf("missing installed xml2 wrapper runtime")
	}
	var values [4]sqltype.Value
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
	return XML2Invoke(c, n.Info, n.Operation, values[:len(n.Args)])
}

// XML2Invoke is the genuine kernel boundary after its caller's STRICT decision.
// A wrapper's exec-owned native body supplies the selected inner Info and expanded
// values; this entry still enters that actual routine, never a name alias.
func XML2Invoke(c expr.Context, info expr.Info, operation XML2Operation, values []sqltype.Value) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	body, err := info.EnterRoutine(c, values)
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	var text [4]string
	for i, v := range values {
		text[i], err = v.Text()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	switch operation {
	case XML2Valid:
		return sqltype.XML2Valid(ctx, info.Usage.Type, text[0], c.FunctionInput())
	case XML2String, XML2Number, XML2Boolean:
		return sqltype.XML2Scalar(ctx, info.Usage.Type, text[0], text[1])
	case XML2Nodeset:
		return sqltype.XML2Set(ctx, info.Usage.Type, text[0], text[1], text[2], text[3], "", false, c.FunctionInput())
	case XML2List:
		return sqltype.XML2Set(ctx, info.Usage.Type, text[0], text[1], "", "", text[2], true, c.FunctionInput())
	case XML2XSLT:
		return sqltype.XML2XSLT(ctx, info.Usage.Type, text[0], text[1], text[2])
	default:
		return sqltype.Value{}, fmt.Errorf("invalid xml2 operation")
	}
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
	m, ok := XML2Lookup(n.Name, len(n.Args))
	if !ok {
		return expr.Volatile
	}
	return m.Volatility
}
func (n XML2) PlanningFold() expr.Fold {
	if n.IntrinsicVolatility() != expr.Immutable {
		return expr.FoldNever
	}
	return expr.FoldConstant
}
func (n XML2) PreflightFold() expr.Preflight {
	if n.IntrinsicVolatility() != expr.Immutable {
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
	return ok && n.Name == o.Name && n.Operation == o.Operation && n.Usage == o.Usage && sameChildren(n.Args, o.Args, same)
}
func (n XML2) Render() (expr.Rendering, error) {
	args := make([]expr.RenderArg, len(n.Args))
	for i, a := range n.Args {
		args[i] = expr.RenderArg{Expr: a}
	}
	return expr.Rendering{Tree: "XML2", Form: expr.FormCall, Name: n.Name, Inputs: n.Args, Args: args}, n.Validate()
}
func (XML2) JumbleFields(s expr.StatementFields) { s.JumbleText("functions.XML2") }
