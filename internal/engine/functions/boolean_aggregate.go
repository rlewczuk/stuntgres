package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

type BooleanAggregateProvider struct {
	usage       sqltype.Usage
	conjunction bool
}
type booleanAggregateState struct{ seen, value bool }

func (p BooleanAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p BooleanAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &booleanAggregateState{}, nil
}
func (p BooleanAggregateProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*booleanAggregateState)
	if !ok {
		return fmt.Errorf("stuntgres: invalid Boolean aggregate state")
	}
	v, err := value.Boolean()
	if err != nil || value.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid Boolean aggregate datum")
	}
	if !s.seen {
		s.seen, s.value = true, v
	} else if p.conjunction {
		s.value = s.value && v
	} else {
		s.value = s.value || v
	}
	return ctx.Err()
}
func (p BooleanAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*booleanAggregateState)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid Boolean aggregate state")
	}
	if !s.seen {
		return sqltype.Null(p.usage.Type), nil
	}
	return sqltype.Boolean(p.usage.Type, s.value), nil
}

func NewBooleanAggregateProvider(usage sqltype.Usage, conjunction bool) BooleanAggregateProvider {
	return BooleanAggregateProvider{usage, conjunction}
}

func (p BooleanAggregateProvider) Conjunction() bool { return p.conjunction }
