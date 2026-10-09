package engine

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/sqltype"
)

// Providers are immutable; states belong exclusively to one execution's group.
// Later aggregate builtins can implement this contract without scalar shortcuts.
type aggregateProvider interface {
	ResultUsage() sqltype.Usage
	Init(context.Context) (any, error)
	Advance(context.Context, any, sqltype.Value) error
	Finalize(context.Context, any) (sqltype.Value, error)
}

type aggregateOperation uint8

const (
	aggregateCount aggregateOperation = iota
	aggregateAnd
	aggregateOr
	aggregateIntervalAvg
	aggregateNumericAvg
	aggregateFloatAvg
	aggregateAnyValue
)

type aggregateCall struct {
	provider  aggregateProvider
	argument  BoundExpr
	operation aggregateOperation
}
type countProvider struct{ usage sqltype.Usage }
type countState struct{ count int64 }

func (p countProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p countProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &countState{}, nil
}
func (p countProvider) Advance(ctx context.Context, state any, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*countState)
	if !ok {
		return fmt.Errorf("stuntgres: invalid count state")
	}
	if s.count == math.MaxInt64 {
		return sqlError("22003", "bigint out of range")
	}
	s.count++
	return ctx.Err()
}
func (p countProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*countState)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid count state")
	}
	return sqltype.Integer(p.usage.Type, s.count)
}

type aggregateExpression struct {
	expressionInfo
	slot int
}

func (e aggregateExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: aggregate occurrence not rewritten")
}

// Group slots have no table address. Proven output origin is retained separately
// by bindSelect, never inferred from this private row layout.
type groupExpression struct {
	expressionInfo
	slot int
}

func (e groupExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.Scope != 1 || c.Frame != 1 || e.slot < 0 || e.slot >= len(c.Row) || c.Row[e.slot].Type() != e.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid group row frame")
	}
	return c.Row[e.slot], nil
}
