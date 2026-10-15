package engine

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"stuntgres/internal/sqltype"
)

// Borrowed only for one finalization; neither provider nor result retains it.
// The sole datum buffer is the executor's ordinary ordered aggregate buffer.
type orderedAggregateInput struct {
	values  []sqltype.Value
	indices []int
	width   int
	order   []sortKey
	target  []sqltype.Value
}

func (in orderedAggregateInput) len() int                  { return len(in.indices) }
func (in orderedAggregateInput) value(i int) sqltype.Value { return in.values[in.indices[i]*in.width] }
func (in orderedAggregateInput) tuple(i int) []sqltype.Value {
	start := in.indices[i] * in.width
	return in.values[start : start+in.width]
}

// A preparation belongs to one group/execution. It retains only the immutable
// direct datum and parsed scalar fraction, never the executor's input buffer.
type orderedSetPreparation struct {
	direct   sqltype.Value
	fraction float64
	value    sqltype.Value
	terminal bool
}

type orderedSetFinalizer interface {
	PrepareOrdered(context.Context, []sqltype.Value, int) (orderedSetPreparation, error)
	FinalizeOrdered(context.Context, orderedSetPreparation, orderedAggregateInput, sqltype.InputEnvironment) (sqltype.Value, error)
}

type orderedSetProvider struct {
	usage                   sqltype.Usage
	input                   *sqltype.Descriptor
	fraction                *sqltype.Descriptor
	operation               aggregateOperation
	array                   bool
	comparison              sqltype.Comparison
	subtract, multiply, add sqltype.Arithmetic
}

func (p *orderedSetProvider) ResultUsage() sqltype.Usage            { return p.usage }
func (p *orderedSetProvider) Init(ctx context.Context) (any, error) { return nil, ctx.Err() }
func (p *orderedSetProvider) Advance(context.Context, any, sqltype.Value, sqltype.Value) error {
	return fmt.Errorf("stuntgres: ordered-set input must use borrowed ordering")
}
func (p *orderedSetProvider) Finalize(context.Context, any) (sqltype.Value, error) {
	return sqltype.Value{}, fmt.Errorf("stuntgres: ordered-set aggregate requires ordered finalization")
}

func orderedSetFraction(value sqltype.Value) (float64, error) {
	fraction, err := value.Float()
	if err != nil {
		return 0, err
	}
	if math.IsNaN(fraction) || fraction < 0 || fraction > 1 {
		text := strconv.FormatFloat(fraction, 'g', 6, 64)
		if math.IsInf(fraction, 1) {
			text = "Infinity"
		}
		if math.IsInf(fraction, -1) {
			text = "-Infinity"
		}
		return 0, sqlError("22003", "percentile value "+text+" is not between 0 and 1")
	}
	return fraction, nil
}

func (p *orderedSetProvider) PrepareOrdered(ctx context.Context, args []sqltype.Value, count int) (orderedSetPreparation, error) {
	if err := ctx.Err(); err != nil {
		return orderedSetPreparation{}, err
	}
	var direct sqltype.Value
	if len(args) > 0 {
		direct = args[0]
	}
	prepared := orderedSetPreparation{direct: direct}
	if p.operation == aggregateMode {
		if count == 0 {
			prepared.terminal = true
			prepared.value = sqltype.Null(p.usage.Type)
		}
		return prepared, ctx.Err()
	}
	if p.array {
		// The direct expression has already run. Empty input wins over array
		// element validation, unlike scalar range validation below.
		if count == 0 || direct.IsNull() {
			prepared.terminal = true
			prepared.value = sqltype.Null(p.usage.Type)
			return prepared, ctx.Err()
		}
		hasFraction := false
		for i := range direct.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return orderedSetPreparation{}, err
			}
			value := direct.ArrayElement(i)
			if !value.IsNull() {
				if _, err := orderedSetFraction(value); err != nil {
					return orderedSetPreparation{}, err
				}
				hasFraction = true
			}
		}
		if !hasFraction {
			// Empty/all-NULL fractions need only their owned result shape.
			// The fill path cannot inspect ordered input for these fractions.
			value, err := p.arrayPercentiles(ctx, direct, orderedAggregateInput{}, sqltype.InputEnvironment{})
			if err != nil {
				return orderedSetPreparation{}, err
			}
			prepared.terminal = true
			prepared.value = value
		}
		return prepared, ctx.Err()
	}
	if direct.IsNull() {
		prepared.terminal = true
		prepared.value = sqltype.Null(p.usage.Type)
		return prepared, ctx.Err()
	}
	fraction, err := orderedSetFraction(direct)
	if err != nil {
		return orderedSetPreparation{}, err
	}
	prepared.fraction = fraction
	if count == 0 {
		prepared.terminal = true
		prepared.value = sqltype.Null(p.usage.Type)
	}
	return prepared, ctx.Err()
}

func (p *orderedSetProvider) FinalizeOrdered(ctx context.Context, prepared orderedSetPreparation, in orderedAggregateInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if p.operation == aggregateMode {
		return p.mode(ctx, in, env)
	}
	if p.array {
		return p.arrayPercentiles(ctx, prepared.direct, in, env)
	}
	return p.percentile(ctx, prepared.fraction, in, env)
}

func (p *orderedSetProvider) arrayPercentiles(ctx context.Context, direct sqltype.Value, in orderedAggregateInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	elements := make([]sqltype.Value, direct.ArrayLen())
	for i := range elements {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		value := direct.ArrayElement(i)
		if value.IsNull() {
			elements[i] = sqltype.Null(p.usage.Type.Element())
			continue
		}
		fraction, err := value.Float()
		if err != nil {
			return sqltype.Value{}, err
		}
		elements[i], err = p.percentile(ctx, fraction, in, env)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	dimensions := make([]sqltype.ArrayDimension, direct.ArrayRank())
	for i := range dimensions {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		dimensions[i] = direct.ArrayDimension(i)
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.OwnedArrayValue(p.usage.Type, dimensions, elements)
}

func (p *orderedSetProvider) percentile(ctx context.Context, fraction float64, in orderedAggregateInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if p.operation == aggregatePercentileDisc {
		index := int(math.Ceil(fraction*float64(in.len()))) - 1
		if index < 0 {
			index = 0
		}
		return in.value(index), nil
	}
	rank := fraction * float64(in.len()-1)
	lo, hi := int(math.Floor(rank)), int(math.Ceil(rank))
	left := in.value(lo)
	if lo == hi {
		return left, nil
	}
	right := in.value(hi)
	proportion := rank - float64(lo)
	if p.input.ID() == sqltype.Float8 {
		x, err := left.Float()
		if err != nil {
			return sqltype.Value{}, err
		}
		y, err := right.Float()
		if err != nil {
			return sqltype.Value{}, err
		}
		// Explicit conversions impose rounding at each released operation;
		// neither fused multiply-add nor weighted endpoints is equivalent.
		delta := float64(y - x)
		product := float64(proportion * delta)
		return sqltype.Float(p.input, float64(x+product))
	}
	delta, err := p.subtract.Binary(ctx, right, left, env)
	if err != nil {
		return sqltype.Value{}, err
	}
	scale, err := sqltype.Float(p.fraction, proportion)
	if err != nil {
		return sqltype.Value{}, err
	}
	product, err := p.multiply.Binary(ctx, delta, scale, env)
	if err != nil {
		return sqltype.Value{}, err
	}
	return p.add.Binary(ctx, product, left, env)
}

func (p *orderedSetProvider) mode(ctx context.Context, in orderedAggregateInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	if in.len() == 0 {
		return sqltype.Null(p.usage.Type), ctx.Err()
	}
	winner, current := in.value(0), in.value(0)
	best, count := 1, 1
	for i := 1; i < in.len(); i++ {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		value := in.value(i)
		comparison, err := p.comparison.Compare(ctx, current, value, env)
		if err != nil {
			return sqltype.Value{}, err
		}
		if comparison == 0 {
			count++
		} else {
			current, count = value, 1
		}
		if count > best {
			winner, best = current, count
		}
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	return winner, nil
}
