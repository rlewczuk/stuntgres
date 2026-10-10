package engine

import (
	"context"
	"fmt"
	"math"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Providers belong to immutable plans; a state belongs to exactly one provider
// and one execution group. Published buffers are never reused by transitions.
type arrayAggregateProvider struct {
	argument *sqltype.Descriptor
	usage    sqltype.Usage
	arrays   bool
}

type arrayAggregateState struct {
	owner      *arrayAggregateProvider
	elements   []sqltype.Value
	dimensions []sqltype.ArrayDimension
	count      int32
	finalized  bool
	result     sqltype.Value
}

func (p *arrayAggregateProvider) ResultUsage() sqltype.Usage { return p.usage }

func (p *arrayAggregateProvider) valid() bool {
	if p == nil || !p.argument.Concrete() || !p.usage.Type.Concrete() || !p.usage.Type.IsArray() {
		return false
	}
	if p.arrays {
		return p.argument == p.usage.Type
	}
	return !p.argument.BaseType().IsArray() && p.usage.Type.Element() == p.argument
}

func (p *arrayAggregateProvider) Init(ctx context.Context) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !p.valid() {
		return nil, fmt.Errorf("stuntgres: invalid array aggregate descriptor")
	}
	return &arrayAggregateState{owner: p}, nil
}

func (p *arrayAggregateProvider) state(state any) (*arrayAggregateState, error) {
	s, ok := state.(*arrayAggregateState)
	if !ok || s == nil || !p.valid() || s.owner != p {
		return nil, fmt.Errorf("stuntgres: invalid array aggregate state")
	}
	return s, nil
}

func (p *arrayAggregateProvider) Advance(ctx context.Context, state any, value, _ sqltype.Value) error {
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

func (p *arrayAggregateProvider) Finalize(ctx context.Context, state any) (sqltype.Value, error) {
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

func (b *expressionBinder) bindArrayAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var small [1]string
	var types []string
	if len(raw.Args) <= 1 {
		types = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		types = make([]string, len(raw.Args))
	}
	var operand comparisonOperand
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		operand = o
		if types != nil {
			types[i] = b.diagnosticTypeName(o.descriptor())
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, usage, arrays, err := b.resolveArrayAggregateSignature(raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	properties := arg.Properties()
	properties.StageDependent = true
	info := b.info(usage, raw.Pos, properties)
	info.collationConflict, info.collationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	operation := aggregateArrayScalar
	if arrays {
		operation = aggregateArrayArray
	}
	call := aggregateCall{
		provider: &arrayAggregateProvider{
			argument: arg.ResultUsage().Type,
			usage:    usage,
			arrays:   arrays,
		},
		argument:     arg,
		operation:    operation,
		includeNulls: true,
	}
	return b.finishAggregate(raw, call, filter, info)
}

func (b *expressionBinder) resolveArrayAggregateSignature(raw parser.AggregateExpr, name parser.ObjectName, operand comparisonOperand, types []string) (BoundExpr, sqltype.Usage, bool, error) {
	if len(raw.Args) > 100 {
		return nil, sqltype.Usage{}, false, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, sqltype.Usage{}, false, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, sqltype.Usage{}, false, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	if !operand.descriptor().Concrete() {
		return nil, sqltype.Usage{}, false, functionResolutionErrorForTypes(name, raw.Pos, types, true)
	}
	arg := operand.node
	usage := arg.ResultUsage()
	usage.Typmod = -1
	arrays := usage.Type.BaseType().IsArray()
	if arrays {
		usage.Type = usage.Type.BaseType()
		arg, err = b.coerceConditional(operand, usage, raw.Name)
		if err != nil {
			return nil, sqltype.Usage{}, false, err
		}
	} else {
		view := CatalogRead{owner: b.engine, state: b.engine.state}
		if b.queryAuth != nil {
			view = b.queryAuth.catalog
		}
		array, ok := view.LookupArray(usage.Type)
		if !ok || !array.Concrete() {
			return nil, sqltype.Usage{}, false, sqlError("42704", "could not find array type for data type "+b.diagnosticTypeName(usage.Type))
		}
		usage.Type = array
	}
	return arg, usage, arrays, nil
}
