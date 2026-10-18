package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type ArrayRandomKind uint8

const (
	ArrayShuffle ArrayRandomKind = iota + 1
	ArraySample
)

func ArrayRandomFunction(name string) ArrayRandomKind {
	switch name {
	case "array_shuffle":
		return ArrayShuffle
	case "array_sample":
		return ArraySample
	}
	return 0
}
func (k ArrayRandomKind) Name() string {
	switch k {
	case ArrayShuffle:
		return "array_shuffle"
	case ArraySample:
		return "array_sample"
	}
	return ""
}
func (k ArrayRandomKind) Arity() int {
	switch k {
	case ArrayShuffle:
		return 1
	case ArraySample:
		return 2
	}
	return 0
}

type ArrayRandom struct {
	expr.Info
	Kind      ArrayRandomKind
	Args      []expr.Expr
	Namespace string
}

func (e ArrayRandom) Validate() error {
	if e.Kind.Arity() == 0 || len(e.Args) != e.Kind.Arity() || e.Usage.Type == nil || !e.Usage.Type.Concrete() || !e.Usage.Type.IsArray() || e.Namespace != "" && e.Namespace != "pg_catalog" {
		return fmt.Errorf("stuntgres: invalid stored array random provider")
	}
	for _, arg := range e.Args {
		if any(arg) == nil {
			return fmt.Errorf("stuntgres: missing array random argument")
		}
	}
	if e.Args[0].ResultUsage().Type != e.Usage.Type {
		return fmt.Errorf("stuntgres: invalid array random input generation")
	}
	if len(e.Args) == 2 {
		d := e.Args[1].ResultUsage().Type
		if d == nil || d.ID() != sqltype.Int4 || !d.Concrete() {
			return fmt.Errorf("stuntgres: invalid array sample count")
		}
	}
	return nil
}
func (e ArrayRandom) Reconstruct(info expr.Info, args []expr.Expr) (ArrayRandom, error) {
	e.Info, e.Args = info, args
	return e, e.Validate()
}

// ArrayRandom implements expr.Composite.
var _ expr.Composite = ArrayRandom{}

func (e ArrayRandom) Children() []expr.Expr { return e.Args }
func (e ArrayRandom) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("ArrayRandom", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}
func (e ArrayRandom) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for _, v := range values[:len(e.Args)] {
		if v.IsNull() {
			return sqltype.Null(e.Usage.Type), nil
		}
	}
	a := values[0]
	total := 0
	if a.ArrayRank() != 0 {
		total = int(a.ArrayDimension(0).Length)
	}
	n := total
	if e.Kind == ArraySample {
		count, err := values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if count < 0 || count > int64(total) {
			return sqltype.Value{}, sqlError("22023", fmt.Sprintf("sample size must be between 0 and %d", total))
		}
		n = int(count)
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if e.Kind == ArrayShuffle && total < 2 {
		return a, nil
	}
	if n == 0 {
		return sqltype.OwnedArrayValue(e.Usage.Type, nil, nil)
	}
	width := a.ArrayLen() / total
	dimensions := a.ArrayDimensions()
	if e.Kind == ArraySample {
		dimensions[0].Lower, dimensions[0].Length = 1, int32(n)
	}
	elements := make([]sqltype.Value, n*width)
	if n == total {
		for i := range elements {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return sqltype.Value{}, err
				}
			}
			elements[i] = a.ArrayElement(i)
		}
	}
	if total == 1 {
		return sqltype.OwnedArrayValue(e.Usage.Type, dimensions, elements)
	}
	owner, err := c.FunctionArrayRandomOwner()
	if err != nil {
		return sqltype.Value{}, err
	}
	random, err := owner.Borrow()
	if err != nil {
		return sqltype.Value{}, err
	}
	defer owner.Release()
	if n == total {
		for i := range n - 1 {
			if err := ctx.Err(); err != nil {
				return sqltype.Value{}, err
			}
			j := i + random.IntN(n-i)
			if i == j {
				continue
			}
			for k := range width {
				if k&255 == 0 {
					if err := ctx.Err(); err != nil {
						return sqltype.Value{}, err
					}
				}
				left, right := i*width+k, j*width+k
				elements[left], elements[right] = elements[right], elements[left]
			}
		}
	} else {
		// Virtual partial Fisher-Yates: selection state scales with n, not total.
		// A single selected slice does not need a position map at all.
		var positions map[int]int
		if n > 1 {
			positions = make(map[int]int, n)
		}
		for i := range n {
			if err := ctx.Err(); err != nil {
				return sqltype.Value{}, err
			}
			j := i + random.IntN(total-i)
			selected, ok := positions[j]
			if !ok {
				selected = j
			}
			if positions != nil {
				replacement, ok := positions[i]
				if !ok {
					replacement = i
				}
				positions[j] = replacement
			}
			for k := range width {
				if k&255 == 0 {
					if err := ctx.Err(); err != nil {
						return sqltype.Value{}, err
					}
				}
				elements[i*width+k] = a.ArrayElement(selected*width + k)
			}
		}
	}
	return sqltype.OwnedArrayValue(e.Usage.Type, dimensions, elements)
}

func (e ArrayRandom) IntrinsicVolatility() expr.Volatility { return expr.Volatile }

// PlanningFold: volatile even for strict NULL, empty and singleton arrays.
func (e ArrayRandom) PlanningFold() expr.Fold { return expr.FoldNever }

func (e ArrayRandom) ValidateStoredProvider() error { return e.Validate() }

var _ expr.Renderable = ArrayRandom{}

// Render implements expr.Renderable.
func (e ArrayRandom) Render() (expr.Rendering, error) {
	return expr.Rendering{Tree: "ArrayRandom " + e.Kind.Name() + " namespace " + e.Namespace, Inputs: e.Args}, nil
}
