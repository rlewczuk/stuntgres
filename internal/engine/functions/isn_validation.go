package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// IsnRuntime borrows only the current execution's physical settings authority.
// No instance of that authority is retained by an installed expression.
type IsnRuntime interface {
	InvokeIsnWeak(bool, bool, *sqltype.Descriptor) (sqltype.Value, error)
}

type IsnValidation struct {
	expr.Info
	Operation       sqltype.ISNOperation
	Args            [1]expr.Expr
	Count           int
	Name, Namespace string
	RoutineRef      ids.ObjectRef
	Formal, Result  sqltype.ID
}

func NewIsnValidation(info expr.Info, operation sqltype.ISNOperation, name, namespace string, ref ids.ObjectRef, args []expr.Expr) (IsnValidation, error) {
	n := IsnValidation{Info: info, Operation: operation, Count: len(args), Name: name, Namespace: namespace, RoutineRef: ref}
	if len(args) > 1 || missingArgument(args) {
		return n, fmt.Errorf("invalid ISN validation operands")
	}
	copy(n.Args[:], args)
	if info.Usage.Type != nil {
		n.Result = info.Usage.Type.ID()
	}
	if len(args) == 1 && args[0].ResultUsage().Type != nil {
		n.Formal = args[0].ResultUsage().Type.ID()
	}
	return n, n.ValidateStoredProvider()
}

func (n IsnValidation) Children() []expr.Expr { return n.Args[:n.Count] }
func (n IsnValidation) ValidateStoredProvider() error {
	if n.Usage.Type == nil || !n.Usage.Valid() || n.Usage.Type.ID() != n.Result || n.Count < 0 || n.Count > 1 || n.Operation > sqltype.ISNWeakSet {
		return fmt.Errorf("invalid ISN validation result")
	}
	if n.Operation == sqltype.ISNWeakGet {
		if n.Count != 0 || n.Usage.Type.ID() != sqltype.Bool {
			return fmt.Errorf("invalid ISN weak getter")
		}
		return nil
	}
	if n.Count != 1 || n.Args[0] == nil || n.Args[0].ResultUsage().Type == nil {
		return fmt.Errorf("invalid ISN validation formal")
	}
	formal := n.Args[0].ResultUsage().Type
	if formal.ID() != n.Formal {
		return fmt.Errorf("ISN validation formal identity changed")
	}
	if n.Operation == sqltype.ISNWeakSet {
		if formal.ID() != sqltype.Bool || n.Usage.Type != formal {
			return fmt.Errorf("invalid ISN weak setter")
		}
	} else if !formal.IsISN() || n.Operation == sqltype.ISNMakeValid && n.Usage != n.Args[0].ResultUsage() || n.Operation == sqltype.ISNIsValid && n.Usage.Type.ID() != sqltype.Bool {
		return fmt.Errorf("invalid ISN validation signature")
	}
	return nil
}
func (n IsnValidation) Eval(c expr.Context) (sqltype.Value, error) {
	if err := c.FunctionContext().Err(); err != nil {
		return sqltype.Value{}, err
	}
	var value sqltype.Value
	if n.Count != 0 {
		var err error
		value, err = n.Args[0].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.Type() != n.Args[0].ResultUsage().Type {
			return sqltype.Value{}, fmt.Errorf("invalid ISN validation datum generation")
		}
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
	}
	if n.RoutineReference != nil {
		facts := expr.RoutineBodyInputs{Count: n.Count, Occurrence: n.Occurrence, Source: n.Source}
		if n.Count != 0 {
			facts.First = value.Type()
		}
		body, err := c.FunctionEnterRoutine(n.RoutineReference, n.RoutineRecipeReference, facts)
		if err != nil {
			return sqltype.Value{}, err
		}
		defer body.Leave()
	}
	switch n.Operation {
	case sqltype.ISNIsValid:
		return sqltype.IsValidISN(value, n.Args[0].ResultUsage(), n.Usage.Type)
	case sqltype.ISNMakeValid:
		return sqltype.MakeValidISN(value, n.Args[0].ResultUsage())
	default:
		runtime, ok := c.(IsnRuntime)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("ISN weak call lacks execution authority")
		}
		if n.Operation == sqltype.ISNWeakSet {
			on, err := value.Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
			return runtime.InvokeIsnWeak(on, true, n.Usage.Type)
		}
		return runtime.InvokeIsnWeak(false, false, n.Usage.Type)
	}
}
func (n IsnValidation) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("ISN validation", children, n.Count); err != nil {
		return nil, err
	}
	if info.Usage.Type == nil || info.Usage.Type.ID() != n.Result || len(children) == 1 && (children[0].ResultUsage().Type == nil || children[0].ResultUsage().Type.ID() != n.Formal) {
		return nil, fmt.Errorf("ISN validation signature changed")
	}
	return NewIsnValidation(info, n.Operation, n.Name, n.Namespace, n.RoutineRef, children)
}
func (n IsnValidation) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return n.Rebuild(info, children)
}
func (n IsnValidation) IntrinsicVolatility() expr.Volatility {
	switch n.Operation {
	case sqltype.ISNWeakGet:
		return expr.Stable
	case sqltype.ISNWeakSet:
		return expr.Volatile
	default:
		return expr.Immutable
	}
}
func (n IsnValidation) PlanningFold() expr.Fold {
	if n.Operation >= sqltype.ISNWeakGet {
		return expr.FoldNever
	}
	return expr.FoldConstant
}
func (IsnValidation) StrictNull() expr.Strict { return expr.StrictAfterBare }
func (IsnValidation) ClosureTransparent()     {}
func (IsnValidation) LooksLikeFunction() bool { return true }
func (n IsnValidation) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(IsnValidation)
	return ok && n.Operation == o.Operation && n.RoutineRef == o.RoutineRef && n.Count == o.Count && sameChildren(n.Children(), o.Children(), same)
}
func (n IsnValidation) Render() (expr.Rendering, error) {
	spec := callRendering("IsnValidation "+n.Namespace+"."+n.Name, n.Children(), n.Name, n.Children())
	transportArgs(spec.Args, expr.TransportLiteral)
	spec.Namespace = n.Namespace
	spec.Replay.QuoteNamespace = true
	spec.Replay.Validate = n.ValidateStoredProvider
	return spec, nil
}
func (n IsnValidation) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: n.Children(), Count: n.Count}, true, nil
}
