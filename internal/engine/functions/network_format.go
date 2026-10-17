package functions

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// NetworkFormat carries one exact scalar signature with inline arguments.
// Type-owned plans retain descriptor authority, not routine ACL snapshots.
type NetworkFormat[E Expression[C], C LiteralContext[E], P any] struct {
	Info[P]
	Kind sqltype.NetworkFormatKind
	Args [2]E
	Plan sqltype.NetworkFormat
}

func NewNetworkFormat[E Expression[C], C LiteralContext[E], P any](info Info[P], kind sqltype.NetworkFormatKind, args [2]E) (NetworkFormat[E, C, P], error) {
	e := NetworkFormat[E, C, P]{Info: info, Kind: kind, Args: args}
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
func (e NetworkFormat[E, C, P]) Reconstruct(info Info[P], args [2]E) (NetworkFormat[E, C, P], error) {
	return NewNetworkFormat[E, C](info, e.Kind, args)
}
func (e NetworkFormat[E, C, P]) Validate() error {
	n, err := e.Reconstruct(e.Info, e.Args)
	if err != nil || n.Plan != e.Plan {
		return fmt.Errorf("stuntgres: invalid stored network formatting provider")
	}
	return nil
}
func (e NetworkFormat[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
func NetworkFormatIntrinsicRule() IntrinsicPropertyRule { return ExactImmutable }
