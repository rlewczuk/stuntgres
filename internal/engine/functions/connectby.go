package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Connectby contains only bound code and its caller-owned anonymous record.
// Traversal and catalog authority belong to the statement runtime.
type Connectby struct {
	expr.Info
	Args                     []expr.Expr
	Ordered, Branch, Defined bool
}

var connectbySignatures = [...]string{"25,25,25,25,23,25", "25,25,25,25,23", "25,25,25,25,25,23,25", "25,25,25,25,25,23"}

func bindConnectby(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
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
	if !raw.ResolutionOnly && !raw.Variadic && !raw.Star && len(raw.ArgNames) == 0 {
		for variant, signature := range connectbySignatures {
			ordered, branch := variant >= 2, variant%2 == 0
			depth := 4
			if ordered {
				depth = 5
			}
			count := depth + 1
			if branch {
				count++
			}
			if len(operands) != count {
				continue
			}
			routine, err := b.InstalledRoutine(raw.Name, TablefuncExtension, signature)
			if err != nil {
				return nil, err
			}
			if routine == nil {
				continue
			}
			valid := true
			for i, arg := range operands {
				target := b.Type(sqltype.Text).Usage()
				if i == depth {
					target = b.Type(sqltype.Int4).Usage()
				}
				if arg.Usage.Type != nil {
					if _, err := sqltype.PlanCoercion(arg.Usage, target, sqltype.Implicit); err != nil {
						valid = false
						break
					}
				}
			}
			if !valid {
				continue
			}
			if err := routine.Validate(); err != nil {
				return nil, err
			}
			if err := CallDecorationError(raw); err != nil {
				return nil, err
			}
			call := Connectby{Args: make([]expr.Expr, count), Ordered: ordered, Branch: branch}
			properties := expr.Properties{Volatility: expr.Stable, StageDependent: true}
			for i, arg := range operands {
				target := b.Type(sqltype.Text).Usage()
				if i == depth {
					target = b.Type(sqltype.Int4).Usage()
				}
				call.Args[i], err = b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: target})
				if err != nil {
					return nil, err
				}
				properties = expr.MergeProperties(properties, call.Args[i].Properties())
			}
			call.Info = b.Info(b.Type(2249).Usage(), raw.Pos, properties)
			return routine.Attach(call)
		}
	}
	return nil, b.SignatureError(raw.Name, raw.Pos, installedArgumentNames(b, raw, operands))
}
func (Connectby) Eval(expr.Context) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: connectby requires a source invocation")
}
func (n Connectby) Children() []expr.Expr { return n.Args }
func (n Connectby) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	depth := 4
	if n.Ordered {
		depth = 5
	}
	count := depth + 1
	if n.Branch {
		count++
	}
	if len(children) != count {
		return nil, fmt.Errorf("stuntgres: invalid connectby arguments")
	}
	for i, child := range children {
		want := sqltype.Text
		if i == depth {
			want = sqltype.Int4
		}
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.BaseType().ID() != want {
			return nil, fmt.Errorf("stuntgres: invalid connectby argument")
		}
	}
	n.Info, n.Args = info, children
	return n, nil
}
func (n Connectby) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (Connectby) SourceAdmission() (bool, bool)        { return true, true }
func (Connectby) IntrinsicVolatility() expr.Volatility { return expr.Stable }
func (Connectby) PlanningFold() expr.Fold              { return expr.FoldNever }
func (Connectby) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (Connectby) LooksLikeFunction() bool              { return true }

type ConnectbyRequest struct {
	Relation, Key, Parent, Order, Start, Delimiter string
	MaxDepth                                       int64
	Ordered, Branch                                bool
	Result                                         *sqltype.Descriptor
	Text                                           *sqltype.Descriptor
	Input                                          sqltype.InputEnvironment
	Output                                         sqltype.OutputEnvironment
}
type ConnectbyRuntime interface {
	Connectby(ConnectbyRequest) ([]sqltype.Value, error)
}

func (n Connectby) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return expr.SourceCursor{}, err
	}
	var scratch [7]sqltype.Value
	values := scratch[:len(n.Args)]
	for i, arg := range n.Args {
		value, err := arg.Eval(ctx)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values[i] = value
	}
	for _, value := range values {
		if value.IsNull() {
			return expr.OwnedSource(n.Usage.Type, nil), nil
		}
	}
	request := ConnectbyRequest{Ordered: n.Ordered, Branch: n.Branch, Delimiter: "~", Result: n.Usage.Type, Text: values[0].Type()}
	targets := [5]*string{&request.Relation, &request.Key, &request.Parent, &request.Start}
	depth := 4
	if n.Ordered {
		targets[3], targets[4], depth = &request.Order, &request.Start, 5
	}
	for i, target := range targets[:depth] {
		text, err := values[i].Text()
		if err != nil {
			return expr.SourceCursor{}, err
		}
		*target = text
	}
	var err error
	request.MaxDepth, err = values[depth].Integer()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if n.Branch {
		request.Delimiter, err = values[depth+1].Text()
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	runtime, ok := ctx.(ConnectbyRuntime)
	if !ok {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing connectby statement runtime")
	}
	rows, err := runtime.Connectby(request)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, rows), nil
}
