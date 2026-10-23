package plan

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// DatumCastExpr retains a selected codec/relabel, never a live catalog lookup.
// Unlike SQLRoutineExpr these two methods are strict.
type DatumCastExpr struct {
	expr.Info
	Child   expr.Expr
	Binary  sqltype.BinaryCoercionPlan
	Method  string
	Context string
	Mode    sqltype.CoercionContext
	Sizing  sqltype.CoercionPlan
	Size    bool
}

func (n DatumCastExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	v, err := n.Child.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	var result sqltype.Value
	if n.Method == "b" {
		result, err = n.Binary.Apply(ctx.FunctionContext(), v)
	} else if v.IsNull() {
		result = sqltype.Null(n.Usage.Type)
	} else {
		var text string
		text, err = v.Type().OutputWithEnvironment(ctx.FunctionContext(), v, ctx.FunctionOutput())
		if err == nil {
			// CoerceViaIO calls input without a typmod. Target sizing is a
			// separate conversion that retains explicit/assignment semantics.
			result, err = n.Usage.Type.InputWithEnvironment(ctx.FunctionContext(), text, -1, ctx.FunctionInput())
		}
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Size {
		return n.Sizing.Apply(ctx.FunctionContext(), result, ctx.FunctionInput(), ctx.FunctionOutput())
	}
	return result, nil
}
func (n DatumCastExpr) Children() []expr.Expr { return []expr.Expr{n.Child} }
func (n DatumCastExpr) Rebuild(info expr.Info, args []expr.Expr) (expr.Expr, error) {
	if n.Method != "b" && n.Method != "i" {
		return nil, fmt.Errorf("invalid cast method")
	}
	if len(args) != 1 || args[0] == nil {
		return nil, fmt.Errorf("invalid cast operands")
	}
	n.Info, n.Child = info, args[0]
	if n.Method == "b" {
		var err error
		n.Binary, err = sqltype.PlanBinaryCoercion(n.Child.ResultUsage().Type, n.Usage.Type)
		if err != nil {
			return nil, err
		}
	}
	n.Sizing = sqltype.CoercionPlan{}
	if n.Usage.Typmod >= 0 {
		var err error
		n.Sizing, err = sqltype.PlanCoercion(n.Usage.Type.Usage(), n.Usage, n.Mode)
		if err != nil {
			return nil, err
		}
		n.Size = true
	} else {
		n.Size = false
	}
	n.Property = expr.MergeProperties(n.Property, n.Child.Properties())
	if volatility := n.IntrinsicVolatility(); n.Property.Volatility < volatility {
		n.Property.Volatility = volatility
	}
	return n, nil
}
func (n DatumCastExpr) ValidateStoredProvider() error {
	if n.Method != "b" && n.Method != "i" {
		return fmt.Errorf("invalid stored cast method")
	}
	_, err := n.Rebuild(n.Info, n.Children())
	return err
}
func (n DatumCastExpr) IntrinsicVolatility() expr.Volatility {
	if n.Method == "i" && (n.Child.ResultUsage().Type.OutputSessionDependent() || n.Usage.Type.InputSessionDependent()) {
		return expr.Stable
	}
	return expr.Immutable
}
func (DatumCastExpr) PlanningFold() expr.Fold { return expr.FoldNever }

func (n DatumCastExpr) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(DatumCastExpr)
	return ok && n.Usage == o.Usage && n.Method == o.Method && n.Context == o.Context &&
		n.Mode == o.Mode && n.Size == o.Size && (!n.Size || n.Sizing.Equal(o.Sizing)) &&
		same(n.Child, o.Child)
}

func NewDatumCast(info expr.Info, child expr.Expr, method string, mode sqltype.CoercionContext) (DatumCastExpr, error) {
	n := DatumCastExpr{Info: info, Child: child, Method: method, Mode: mode}
	rebuilt, err := n.Rebuild(info, n.Children())
	if err != nil {
		return DatumCastExpr{}, err
	}
	return rebuilt.(DatumCastExpr), nil
}

func (DatumCastExpr) ClosureTransparent() {}
