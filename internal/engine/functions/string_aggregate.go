package functions

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type StringAggregateProvider struct{ usage sqltype.Usage }
type stringAggregateState struct {
	typ    *sqltype.Descriptor
	seen   bool
	output strings.Builder
}

func (p StringAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }
func (p StringAggregateProvider) valid() bool {
	return p.usage.Type != nil && (p.usage.Type.ID() == sqltype.Text || p.usage.Type.ID() == sqltype.Bytea)
}
func (p StringAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.valid() {
		return nil, fmt.Errorf("stuntgres: invalid string aggregate descriptor")
	}
	return &stringAggregateState{typ: p.usage.Type}, nil
}
func (p StringAggregateProvider) Advance(ctx context.Context, state any, values []sqltype.Value, _ expr.Context) error {
	value := values[0]
	separator := values[1]
	if err := ctx.Err(); err != nil {
		return err
	}
	s, ok := state.(*stringAggregateState)
	if !ok || s == nil || !p.valid() || s.typ != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid string aggregate state")
	}
	if value.Type() != p.usage.Type || separator.Type() != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid string aggregate datum")
	}
	if value.IsNull() {
		return nil
	}
	payload := func(v sqltype.Value) (string, error) {
		if p.usage.Type.ID() == sqltype.Bytea {
			return v.Binary()
		}
		return v.Text()
	}
	v, err := payload(value)
	if err != nil {
		return err
	}
	var d string
	if s.seen && !separator.IsNull() {
		d, err = payload(separator)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.output.WriteString(d)
	s.output.WriteString(v)
	s.seen = true
	return ctx.Err()
}
func (p StringAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, ok := state.(*stringAggregateState)
	if !ok || s == nil || !p.valid() || s.typ != p.usage.Type {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid string aggregate state")
	}
	if !s.seen {
		return sqltype.Null(p.usage.Type), nil
	}
	if p.usage.Type.ID() == sqltype.Bytea {
		return sqltype.Binary(p.usage.Type, s.output.String())
	}
	return sqltype.String(p.usage.Type, s.output.String()), nil
}

func NewStringAggregateProvider(usage sqltype.Usage) StringAggregateProvider {
	return StringAggregateProvider{usage}
}
