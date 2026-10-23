package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"

	"stuntgres/internal/sqltype"
)

type AnyValue struct{ usage sqltype.Usage }
type anyValueState struct{ value sqltype.Value }

func NewAnyValue(usage sqltype.Usage) AnyValue              { return AnyValue{usage} }
func (p AnyValue) Reconstruct(usage sqltype.Usage) AnyValue { return AnyValue{usage} }
func (p AnyValue) ResultUsage() sqltype.Usage               { return p.usage }
func (p AnyValue) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.usage.Type.Concrete() {
		return nil, fmt.Errorf("stuntgres: invalid any_value result type")
	}
	return &anyValueState{value: sqltype.Null(p.usage.Type)}, nil
}
func (p AnyValue) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	value := values[0]
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*anyValueState)
	if !ok || s == nil || !p.usage.Type.Concrete() || s.value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid any_value state")
	}
	// Validate later candidates too. Retention does not end argument evaluation
	// or relax the execution's concrete descriptor ownership contract.
	if value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid any_value datum")
	}
	if s.value.IsNull() && !value.IsNull() {
		s.value = value
	}
	return ctx.Err()
}
func (p AnyValue) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*anyValueState)
	if !ok || s == nil || !p.usage.Type.Concrete() || s.value.Type() != p.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid any_value state")
	}
	return s.value, nil
}
