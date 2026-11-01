package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// TablefuncCrosstab owns only the admitted arguments and declared result shape.
// SQL execution and materialization belong to the current statement host.
type TablefuncCrosstab struct {
	expr.Info
	Args []expr.Expr
	Categories bool
}

func bindTablefuncCrosstab(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	operands := make([]Argument, len(raw.Args))
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		operands[i] = arg
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	routine, err := b.InstalledRoutine(raw.Name, TablefuncExtension, "")
	if err != nil {
		return nil, err
	}
	if routine == nil {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	declaration, ok := routine.(interface{ TablefuncCrosstabSignature() string })
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing crosstab declaration")
	}
	signature := declaration.TablefuncCrosstabSignature()
	categories := signature == "25,25"
	arity := 1
	if signature == "25,23" || categories {
		arity = 2
	}
	valid := (signature == "25" || signature == "25,23" || categories) && !raw.ResolutionOnly && !raw.Variadic && len(operands) == arity && len(raw.ArgNames) == 0
	args := make([]expr.Expr, len(operands))
	properties := expr.Properties{Volatility: expr.Stable, StageDependent: true}
	for i, operand := range operands {
		id := sqltype.Text
		if i == 1 && !categories {
			id = sqltype.Int4
		}
		formal := b.Type(id).Usage()
		if operand.Usage.Type != nil {
			if _, e := sqltype.PlanCoercion(operand.Usage, formal, sqltype.Implicit); e != nil {
				valid = false
			}
		}
		if valid {
			args[i], err = b.CoerceArgument(operand, CoercionRequest{Intent: DirectFormalArgument, Target: formal})
			if err != nil {
				return nil, err
			}
			properties = expr.MergeProperties(properties, args[i].Properties())
		}
	}
	if !valid {
		return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	return routine.Attach(TablefuncCrosstab{Info: b.Info(b.Type(2249).Usage(), raw.Pos, properties), Args: args, Categories: categories})
}
func (n TablefuncCrosstab) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: crosstab requires a source invocation")
}
func (n TablefuncCrosstab) Children() []expr.Expr { return n.Args }
func (n TablefuncCrosstab) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(n.Args) || len(children) < 1 || len(children) > 2 || n.Categories && len(children) != 2 {
		return nil, fmt.Errorf("stuntgres: invalid crosstab arguments")
	}
	for i, child := range children {
		id := sqltype.Text
		if i == 1 && !n.Categories {
			id = sqltype.Int4
		}
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.ID() != id {
			return nil, fmt.Errorf("stuntgres: invalid crosstab argument")
		}
	}
	n.Info, n.Args = info, append([]expr.Expr(nil), children...)
	return n, nil
}
func (n TablefuncCrosstab) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (TablefuncCrosstab) SourceAdmission() (bool, bool) { return true, true }

type TablefuncCrosstabRuntime interface {
	TablefuncCrosstab(string, string, bool, *sqltype.Descriptor) ([]sqltype.Value, error)
}

func (n TablefuncCrosstab) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	var source, categories string
	null := false
	for i, arg := range n.Args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		if value.IsNull() {
			null = true
		} else if i == 0 {
			source, err = value.Text()
			if err != nil {
				return expr.SourceCursor{}, err
			}
		} else if n.Categories && i == 1 {
			categories, err = value.Text()
			if err != nil {
				return expr.SourceCursor{}, err
			}
		}
	}
	if null {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	if n.RoutineReference != nil {
		body, err := ctx.FunctionEnterRoutine(n.RoutineReference, n.RoutineRecipeReference, expr.RoutineBodyInputs{Count: len(n.Args), First: n.Args[0].ResultUsage().Type, Occurrence: n.Occurrence, Source: n.Source})
		if err != nil {
			return expr.SourceCursor{}, err
		}
		defer body.Leave()
	}
	if n.Usage.Type == nil || !n.Usage.Type.IsComposite() {
		if n.Categories {
			return expr.SourceCursor{}, expr.ErrorAt("0A000", "materialize mode required, but it is not allowed in this context", parser.Position{})
		}
		return expr.SourceCursor{}, expr.ErrorAt("0A000", "function returning record called in context that cannot accept type record", parser.Position{})
	}
	runtime, ok := ctx.(TablefuncCrosstabRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing crosstab statement runtime")
	}
	rows, err := runtime.TablefuncCrosstab(source, categories, n.Categories, n.Usage.Type)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, rows), nil
}
func (TablefuncCrosstab) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (TablefuncCrosstab) PlanningFold() expr.Fold              { return expr.FoldNever }
func (TablefuncCrosstab) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (TablefuncCrosstab) LooksLikeFunction() bool              { return true }
