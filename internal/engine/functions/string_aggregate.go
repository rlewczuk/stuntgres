package functions

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type StringAggregateProvider struct{ usage sqltype.Usage }
type stringAggregateState struct {
	typ    *sqltype.Descriptor
	seen   bool
	cursor int
	output []byte
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
	if !separator.IsNull() {
		d, err = payload(separator)
		if err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.seen {
		s.cursor = len(d)
	}
	s.output = append(s.output, d...)
	s.output = append(s.output, v...)
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
		return sqltype.Binary(p.usage.Type, string(s.output[s.cursor:]))
	}
	return sqltype.String(p.usage.Type, string(s.output[s.cursor:])), nil
}

func NewStringAggregateProvider(usage sqltype.Usage) StringAggregateProvider {
	return StringAggregateProvider{usage}
}

func (p StringAggregateProvider) PartialCapable() bool { return p.valid() }

func (p StringAggregateProvider) Combine(ctx context.Context, destination, incoming any, _ expr.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	left, lok := destination.(*stringAggregateState)
	right, rok := incoming.(*stringAggregateState)
	if !lok || !rok || left == nil || right == nil || left == right || left.typ != p.usage.Type || right.typ != p.usage.Type {
		return fmt.Errorf("stuntgres: invalid string combine state")
	}
	if !right.seen {
		return nil
	}
	if !left.seen {
		left.cursor = right.cursor
	}
	left.output = append(left.output, right.output...)
	left.seen = true
	return ctx.Err()
}

func (p StringAggregateProvider) Serialize(ctx context.Context, opaque any) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s, ok := opaque.(*stringAggregateState)
	if !ok || s == nil || s.typ != p.usage.Type || s.cursor < 0 || s.cursor > len(s.output) || uint64(s.cursor) > math.MaxInt32 {
		return nil, false, fmt.Errorf("stuntgres: invalid string serialization state")
	}
	if !s.seen {
		return nil, false, nil
	}
	data := make([]byte, 4+len(s.output))
	binary.BigEndian.PutUint32(data, uint32(s.cursor))
	copy(data[4:], s.output)
	return data, true, ctx.Err()
}

// The SQL deserializer's nonnull dummy internal pointer is implicit in this
// private invocation. present denotes the transported state, never a SQL NULL
// dummy argument; every present payload executes the real decoder.

func (p StringAggregateProvider) Deserialize(ctx context.Context, data []byte, present bool) (any, error) {
	state, err := p.Init(ctx)
	if err != nil {
		return nil, err
	}
	s := state.(*stringAggregateState)
	if !present {
		if len(data) != 0 {
			return nil, fmt.Errorf("stuntgres: payload for absent string state")
		}
		return s, nil
	}
	if len(data) < 4 {
		return nil, fmt.Errorf("stuntgres: truncated string state")
	}
	cursor := binary.BigEndian.Uint32(data)
	if cursor > math.MaxInt32 || uint64(cursor) > uint64(len(data)-4) {
		return nil, fmt.Errorf("stuntgres: invalid string state cursor")
	}
	s.cursor, s.seen = int(cursor), true
	s.output = append(s.output, data[4:]...)
	return s, ctx.Err()
}
