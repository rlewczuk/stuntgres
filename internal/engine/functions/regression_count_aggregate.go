package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

// RegressionCountAggregate counts admitted pairs without inspecting their payloads
// or computing floating moments. Input and result descriptors belong to its catalog.
type RegressionCountAggregate struct {
	input *sqltype.Descriptor
	usage sqltype.Usage
}

func NewRegressionCountAggregate(input *sqltype.Descriptor, usage sqltype.Usage) RegressionCountAggregate {
	return RegressionCountAggregate{input, usage}
}
func (p RegressionCountAggregate) InputType() *sqltype.Descriptor { return p.input }
func (p RegressionCountAggregate) ResultUsage() sqltype.Usage     { return p.usage }
func (p RegressionCountAggregate) Reconstruct(input *sqltype.Descriptor, usage sqltype.Usage) RegressionCountAggregate {
	return NewRegressionCountAggregate(input, usage)
}
func (p RegressionCountAggregate) Init(ctx context.Context) (any, error) {
	return NewCountProvider(p.usage).Init(ctx)
}
func (p RegressionCountAggregate) Advance(ctx context.Context, state any, y, x sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*countState)
	if !ok || s == nil {
		return fmt.Errorf("stuntgres: invalid regression count state")
	}
	if y.Type() == nil || x.Type() == nil || y.Type() != p.input || x.Type() != p.input {
		return fmt.Errorf("stuntgres: invalid regression count datum")
	}
	if y.IsNull() || x.IsNull() {
		return ctx.Err()
	}
	return NewCountProvider(p.usage).Advance(ctx, s, y, x)
}
func (p RegressionCountAggregate) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*countState)
	if !ok || s == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid regression count state")
	}
	value, err := NewCountProvider(p.usage).Finalize(ctx, s)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return value, nil
}
