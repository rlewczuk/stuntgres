package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// NetworkFormat carries one exact scalar signature with inline arguments.
// Type-owned plans retain descriptor authority, not routine ACL snapshots.
type NetworkFormat struct {
	expr.Info
	Kind sqltype.NetworkFormatKind
	Args [2]expr.Expr
	Plan sqltype.NetworkFormat
}

func NewNetworkFormat(info expr.Info, kind sqltype.NetworkFormatKind, args [2]expr.Expr) (NetworkFormat, error) {
	e := NetworkFormat{Info: info, Kind: kind, Args: args}
	if !info.Usage.Valid() || info.Usage != info.Usage.Type.Usage() || kind.Arity() == 0 {
		return e, fmt.Errorf("stuntgres: invalid network formatting result")
	}
	var types [2]*sqltype.Descriptor
	for i, arg := range args {
		if i >= kind.Arity() {
			if any(arg) != nil {
				return e, fmt.Errorf("stuntgres: extra network formatting argument")
			}
			continue
		}
		if any(arg) == nil {
			return e, fmt.Errorf("stuntgres: missing network formatting argument")
		}
		u := arg.ResultUsage()
		if !u.Valid() || u != u.Type.Usage() {
			return e, fmt.Errorf("stuntgres: invalid network formatting argument")
		}
		types[i] = u.Type
	}
	plan, err := sqltype.BindNetworkFormat(kind, info.Usage.Type, types[0], types[1])
	e.Plan = plan
	return e, err
}
func (e NetworkFormat) Reconstruct(info expr.Info, args [2]expr.Expr) (NetworkFormat, error) {
	return NewNetworkFormat(info, e.Kind, args)
}

// NetworkFormat implements expr.Composite.
var _ expr.Composite = NetworkFormat{}

func (e NetworkFormat) Children() []expr.Expr { return e.Args[:e.Kind.Arity()] }
func (e NetworkFormat) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("NetworkFormat", children, e.Kind.Arity()); err != nil {
		return nil, err
	}
	args := e.Args
	copy(args[:], children)
	return rebuilt(e.Reconstruct(info, args))
}
func (e NetworkFormat) Validate() error {
	n, err := e.Reconstruct(e.Info, e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored network formatting provider")
	}
	return nil
}
func (e NetworkFormat) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, arg := range e.Args[:e.Kind.Arity()] {
		if c.FunctionLiteralNull(arg) {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args[:e.Kind.Arity()] {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	if e.Kind.Arity() == 1 {
		return e.Plan.Unary(ctx, values[0])
	}
	return e.Plan.Binary(ctx, values[0], values[1])
}
func NetworkFormatIntrinsicRule() expr.IntrinsicPropertyRule { return expr.ExactImmutable }

func (e NetworkFormat) IntrinsicVolatility() expr.Volatility { return expr.Immutable }

func (e NetworkFormat) ValidateStoredProvider() error { return e.Validate() }

var _ expr.Renderable = NetworkFormat{}

// Render implements expr.Renderable.
func (e NetworkFormat) Render() (expr.Rendering, error) {
	args := e.Args[:e.Kind.Arity()]
	spec := callRendering("NetworkFormat "+e.Kind.Name(), args, e.Kind.Name(), args)
	spec.Replay.Validate = e.Validate
	return spec, nil
}
