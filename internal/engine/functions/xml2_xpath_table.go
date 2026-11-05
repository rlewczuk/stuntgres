package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// XML2XPathTable retains five written-order TEXT operands and the admitted
// record occurrence. Dynamic SQL is resolved only by the executing statement.
type XML2XPathTable struct {
	expr.Info
	Args []expr.Expr
}

func bindXML2XPathTable(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands []Argument
	if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = arg
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, XML2Extension, "25,25,25,25,25")
	if err != nil {
		return nil, err
	}
	valid := routine != nil && len(operands) == 5 && len(raw.ArgNames) == 0 && !raw.Variadic && !raw.Star && !raw.ResolutionOnly
	target := b.Type(sqltype.Text).Usage()
	for _, operand := range operands {
		if operand.Usage.Type != nil && !b.CanCoerce(operand.Usage, target, sqltype.Implicit) {
			valid = false
		}
	}
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	args := make([]expr.Expr, 5)
	properties := expr.Properties{Volatility: expr.Stable, StageDependent: true}
	for i, operand := range operands {
		args[i], err = b.CoerceArgument(operand, CoercionRequest{Intent: DirectFormalArgument, Target: target, AtRaw: true})
		if err != nil {
			return nil, err
		}
		properties = expr.MergeProperties(properties, args[i].Properties())
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	return routine.Attach(XML2XPathTable{Info: b.Info(b.Type(2249).Usage(), raw.Pos, properties), Args: args})
}

func (n XML2XPathTable) Validate() error {
	if !n.Usage.Valid() || n.Usage.Type.ID() != 2249 || len(n.Args) != 5 {
		return fmt.Errorf("invalid xpath_table expression")
	}
	for _, arg := range n.Args {
		if arg == nil || !arg.ResultUsage().Valid() || arg.ResultUsage().Type.ID() != sqltype.Text {
			return fmt.Errorf("invalid xpath_table operand")
		}
	}
	return nil
}
func (n XML2XPathTable) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("xpath_table requires a source invocation")
}
func (n XML2XPathTable) Children() []expr.Expr { return n.Args }
func (n XML2XPathTable) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	n.Info, n.Args = info, append([]expr.Expr(nil), children...)
	return n, n.Validate()
}
func (n XML2XPathTable) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (XML2XPathTable) SourceAdmission() (bool, bool) { return true, true }

type XML2XPathTableRuntime interface {
	XML2XPathTable([5]string, *sqltype.Descriptor, expr.RoutineReference) ([]sqltype.Value, error)
}

func (n XML2XPathTable) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if err := n.Validate(); err != nil {
		return expr.SourceCursor{}, err
	}
	var values [5]sqltype.Value
	var args [5]string
	null := false
	for i, arg := range n.Args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values[i] = value
		if value.IsNull() {
			null = true
		} else {
			args[i], err = value.Text()
			if err != nil {
				return expr.SourceCursor{}, err
			}
		}
	}
	if null {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	body, err := n.EnterRoutine(ctx, values[:])
	if err != nil {
		return expr.SourceCursor{}, err
	}
	defer body.Leave()
	if !n.Usage.Type.IsComposite() || n.Usage.Type.CompositeLen() == 0 {
		return expr.SourceCursor{}, expr.ErrorAt("0A000", "materialize mode required, but it is not allowed in this context", parser.Position{})
	}
	runtime, ok := ctx.(XML2XPathTableRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("missing xpath_table statement runtime")
	}
	rows, err := runtime.XML2XPathTable(args, n.Usage.Type, n.RoutineReference)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, rows), nil
}
func (XML2XPathTable) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (XML2XPathTable) PlanningFold() expr.Fold              { return expr.FoldNever }
func (XML2XPathTable) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (XML2XPathTable) LooksLikeFunction() bool              { return true }
