package functions

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/sqltype"
)

// Providers belong to immutable plans; a state belongs to exactly one provider
// and one execution group. Published buffers are never reused by transitions.
type ArrayAggregateProvider struct {
	argument *sqltype.Descriptor
	usage    sqltype.Usage
	arrays   bool
}

type arrayAggregateState struct {
	owner      *ArrayAggregateProvider
	elements   []sqltype.Value
	dimensions []sqltype.ArrayDimension
	count      int32
	finalized  bool
	result     sqltype.Value
}

func (p *ArrayAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }

func (p *ArrayAggregateProvider) valid() bool {
	if p == nil || !p.argument.Concrete() || !p.usage.Type.Concrete() || !p.usage.Type.IsArray() {
		return false
	}
	if p.arrays {
		return p.argument == p.usage.Type
	}
	return !p.argument.BaseType().IsArray() && p.usage.Type.Element() == p.argument
}

func (p *ArrayAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.valid() {
		return nil, fmt.Errorf("stuntgres: invalid array aggregate descriptor")
	}
	return &arrayAggregateState{owner: p}, nil
}

func (p *ArrayAggregateProvider) state(state any) (*arrayAggregateState, error) {
	s, ok := state.(*arrayAggregateState)
	if !ok || s == nil || !p.valid() || s.owner != p {
		return nil, fmt.Errorf("stuntgres: invalid array aggregate state")
	}
	return s, nil
}

func (p *ArrayAggregateProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := p.state(state)
	if err != nil {
		return err
	}
	if s.finalized {
		return fmt.Errorf("stuntgres: array aggregate transition after finalization")
	}
	if value.Type() != p.argument {
		return fmt.Errorf("stuntgres: invalid array aggregate datum")
	}
	if s.count == math.MaxInt32-1 {
		return sqlError("54000", "array size exceeds the maximum allowed")
	}
	if !p.arrays {
		s.elements = append(s.elements, value)
		s.count++
		return ctx.Err()
	}
	if value.IsNull() {
		return sqlError("22004", "cannot accumulate null arrays")
	}
	rank := value.ArrayRank()
	if s.count == 0 {
		if rank == 0 {
			return sqlError("2202E", "cannot accumulate empty arrays")
		}
		if rank >= 6 {
			return sqlError("54000", fmt.Sprintf("number of array dimensions (%d) exceeds the maximum allowed (6)", rank+1))
		}
		s.dimensions = make([]sqltype.ArrayDimension, rank+1)
		s.dimensions[0].Lower = 1
		for i := range rank {
			if err := ctx.Err(); err != nil {
				return err
			}
			s.dimensions[i+1] = value.ArrayDimension(i)
		}
	} else {
		if rank != len(s.dimensions)-1 {
			return sqlError("2202E", "cannot accumulate arrays of different dimensionality")
		}
		for i := range rank {
			if err := ctx.Err(); err != nil {
				return err
			}
			if value.ArrayDimension(i) != s.dimensions[i+1] {
				return sqlError("2202E", "cannot accumulate arrays of different dimensionality")
			}
		}
	}
	if value.ArrayLen() > math.MaxInt32-len(s.elements) {
		return sqlError("54000", "array size exceeds the maximum allowed")
	}
	for i := range value.ArrayLen() {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.elements = append(s.elements, value.ArrayElement(i))
	}
	s.count++
	return ctx.Err()
}

func (p *ArrayAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s, err := p.state(state)
	if err != nil {
		return sqltype.Value{}, err
	}
	if s.finalized {
		return s.result, nil
	}
	result := sqltype.Null(p.usage.Type)
	if s.count != 0 {
		if !p.arrays {
			s.dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: s.count}}
		} else {
			s.dimensions[0].Length = s.count
		}
		result, err = sqltype.OwnedArrayValue(p.usage.Type, s.dimensions, s.elements)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	s.result, s.finalized = result, true
	// Transfer exclusively owned buffers once. Only the immutable result retains
	// their backing storage; repeated Finalize returns the same publication.
	s.elements, s.dimensions = nil, nil
	return result, nil
}

func NewArrayAggregateProvider(argument *sqltype.Descriptor, usage sqltype.Usage, arrays bool) *ArrayAggregateProvider {
	return &ArrayAggregateProvider{argument: argument, usage: usage, arrays: arrays}
}
func (p *ArrayAggregateProvider) ArgumentType() *sqltype.Descriptor { return p.argument }
func (p *ArrayAggregateProvider) Arrays() bool                      { return p.arrays }
