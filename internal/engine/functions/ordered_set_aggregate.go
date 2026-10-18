package functions

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"stuntgres/internal/sqltype"
)

// OrderedInput is an ordered-set aggregate's sorted group input, borrowed
// only for one finalization; neither provider nor result retains it. Values
// is the executor's ordinary ordered aggregate buffer: Width staged values
// per input row, in Indices order. Target is the hypothetical row, and
// Compare is the call's ORDER BY tuple comparison.
type OrderedInput struct {
	Values  []sqltype.Value
	Indices []int
	Width   int
	Target  []sqltype.Value
	Compare func(ctx context.Context, left, right []sqltype.Value, env sqltype.InputEnvironment) (int, error)
}

func (in OrderedInput) Len() int                  { return len(in.Indices) }
func (in OrderedInput) Value(i int) sqltype.Value { return in.Values[in.Indices[i]*in.Width] }
func (in OrderedInput) Tuple(i int) []sqltype.Value {
	start := in.Indices[i] * in.Width
	return in.Values[start : start+in.Width]
}

// OrderedPreparation belongs to one group and execution. It retains only the
// immutable direct datum and parsed scalar fraction, never the executor's
// input buffer. A Terminal preparation already holds the result Value, and
// the executor skips sorting.
type OrderedPreparation struct {
	direct   sqltype.Value
	fraction float64
	Value    sqltype.Value
	Terminal bool
}

// OrderedAggregate is implemented by WITHIN GROUP providers: the executor
// evaluates their direct arguments over the group keys, prepares, then
// sorts the staged input and finalizes over it instead of Advance/Finalize.
type OrderedAggregate interface {
	PrepareOrdered(context.Context, []sqltype.Value, int) (OrderedPreparation, error)
	FinalizeOrdered(context.Context, OrderedPreparation, OrderedInput, sqltype.InputEnvironment) (sqltype.Value, error)
}

type OrderedSet struct {
	usage                   sqltype.Usage
	input                   *sqltype.Descriptor
	fraction                *sqltype.Descriptor
	operation               AggregateOperation
	array                   bool
	comparison              sqltype.Comparison
	subtract, multiply, add sqltype.Arithmetic
}

func (p *OrderedSet) ResultUsage() sqltype.Usage            { return p.usage }
func (p *OrderedSet) Init(ctx context.Context) (any, error) { return nil, ctx.Err() }
func (p *OrderedSet) Advance(context.Context, any, sqltype.Value, sqltype.Value) error {
	return fmt.Errorf("stuntgres: ordered-set input must use borrowed ordering")
}
func (p *OrderedSet) Finalize(context.Context, any) (sqltype.Value, error) {
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

func (p *OrderedSet) PrepareOrdered(ctx context.Context, args []sqltype.Value, count int) (OrderedPreparation, error) {
	if err := ctx.Err(); err != nil {
		return OrderedPreparation{}, err
	}
	var direct sqltype.Value
	if len(args) > 0 {
		direct = args[0]
	}
	prepared := OrderedPreparation{direct: direct}
	if p.operation == AggregateMode {
		if count == 0 {
			prepared.Terminal = true
			prepared.Value = sqltype.Null(p.usage.Type)
		}
		return prepared, ctx.Err()
	}
	if p.array {
		// The direct expression has already run. Empty input wins over array
		// element validation, unlike scalar range validation below.
		if count == 0 || direct.IsNull() {
			prepared.Terminal = true
			prepared.Value = sqltype.Null(p.usage.Type)
			return prepared, ctx.Err()
		}
		hasFraction := false
		for i := range direct.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return OrderedPreparation{}, err
			}
			value := direct.ArrayElement(i)
			if !value.IsNull() {
				if _, err := orderedSetFraction(value); err != nil {
					return OrderedPreparation{}, err
				}
				hasFraction = true
			}
		}
		if !hasFraction {
			// Empty/all-NULL fractions need only their owned result shape.
			// The fill path cannot inspect ordered input for these fractions.
			value, err := p.arrayPercentiles(ctx, direct, OrderedInput{}, sqltype.InputEnvironment{})
			if err != nil {
				return OrderedPreparation{}, err
			}
			prepared.Terminal = true
			prepared.Value = value
		}
		return prepared, ctx.Err()
	}
	if direct.IsNull() {
		prepared.Terminal = true
		prepared.Value = sqltype.Null(p.usage.Type)
		return prepared, ctx.Err()
	}
	fraction, err := orderedSetFraction(direct)
	if err != nil {
		return OrderedPreparation{}, err
	}
	prepared.fraction = fraction
	if count == 0 {
		prepared.Terminal = true
		prepared.Value = sqltype.Null(p.usage.Type)
	}
	return prepared, ctx.Err()
}

func (p *OrderedSet) FinalizeOrdered(ctx context.Context, prepared OrderedPreparation, in OrderedInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if p.operation == AggregateMode {
		return p.mode(ctx, in, env)
	}
	if p.array {
		return p.arrayPercentiles(ctx, prepared.direct, in, env)
	}
	return p.percentile(ctx, prepared.fraction, in, env)
}

func (p *OrderedSet) arrayPercentiles(ctx context.Context, direct sqltype.Value, in OrderedInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
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

func (p *OrderedSet) percentile(ctx context.Context, fraction float64, in OrderedInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if p.operation == AggregatePercentileDisc {
		index := int(math.Ceil(fraction*float64(in.Len()))) - 1
		if index < 0 {
			index = 0
		}
		return in.Value(index), nil
	}
	rank := fraction * float64(in.Len()-1)
	lo, hi := int(math.Floor(rank)), int(math.Ceil(rank))
	left := in.Value(lo)
	if lo == hi {
		return left, nil
	}
	right := in.Value(hi)
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

func (p *OrderedSet) mode(ctx context.Context, in OrderedInput, env sqltype.InputEnvironment) (sqltype.Value, error) {
	if in.Len() == 0 {
		return sqltype.Null(p.usage.Type), ctx.Err()
	}
	winner, current := in.Value(0), in.Value(0)
	best, count := 1, 1
	for i := 1; i < in.Len(); i++ {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		value := in.Value(i)
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

// NewOrderedSet builds percentile_cont, percentile_disc or mode over the
// resolved input type; interval percentile_cont binds its interpolation.
func NewOrderedSet(usage sqltype.Usage, input, fraction *sqltype.Descriptor, operation AggregateOperation, array bool, comparison sqltype.Comparison) (*OrderedSet, error) {
	p := &OrderedSet{usage: usage, input: input, fraction: fraction, operation: operation, array: array, comparison: comparison}
	if operation != AggregatePercentileCont || input.ID() != sqltype.Interval {
		return p, nil
	}
	var err error
	p.subtract, err = sqltype.BindArithmetic(sqltype.ArithmeticSubtract, input, input, input)
	if err == nil {
		p.multiply, err = sqltype.BindArithmetic(sqltype.ArithmeticMultiply, input, fraction, input)
	}
	if err == nil {
		p.add, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, input, input, input)
	}
	return p, err
}

func (p *OrderedSet) InputType() *sqltype.Descriptor    { return p.input }
func (p *OrderedSet) FractionType() *sqltype.Descriptor { return p.fraction }
func (p *OrderedSet) Operation() AggregateOperation     { return p.operation }
func (p *OrderedSet) Array() bool                       { return p.array }
func (p *OrderedSet) Comparison() sqltype.Comparison    { return p.comparison }
