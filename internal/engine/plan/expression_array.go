package plan

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Array target plans retain only analysis state. Every bound and RHS is evaluated
// against the original execution frame, while writes compose into a private row.
type ArrayAssignmentTarget struct {
	Node        SubscriptExpr
	Container   sqltype.Usage
	Destination sqltype.Usage
	Reapply     sqltype.CoercionPlan
	Name        string
	Pos         parser.Position
	Attribute   int32
	Next        *ArrayAssignmentTarget
}

func (t *ArrayAssignmentTarget) Column(original catalog.Column) catalog.Column {
	original.Usage = t.Destination
	return original
}

func (t *ArrayAssignmentTarget) Apply(ctx expr.Context, current, rhs sqltype.Value) (sqltype.Value, error) {
	return t.applyPath(ctx, current, nil, rhs)
}

func (t *ArrayAssignmentTarget) ApplyExpression(ctx expr.Context, current sqltype.Value, rhs expr.Expr) (sqltype.Value, error) {
	return t.applyPath(ctx, current, rhs, sqltype.Value{})
}

func (t *ArrayAssignmentTarget) applyPath(ctx expr.Context, current sqltype.Value, expression expr.Expr, rhs sqltype.Value) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	var storage [6]sqltype.ArraySubscriptValue
	bounds, err := evaluateArrayBounds(ctx, t.Node.Subscripts, t.Node.Slice, storage[:len(t.Node.Subscripts)])
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, bound := range bounds {
		if bound.LowerNull || bound.UpperNull {
			return sqltype.Value{}, sqlError("22004", "array subscript in assignment must not be null")
		}
	}
	if t.Next != nil {
		var previous sqltype.Value
		if t.Attribute != 0 {
			index, ok := current.Type().BaseType().CompositeAttributeIndex(t.Attribute)
			if !ok {
				return sqltype.Value{}, fmt.Errorf("stuntgres: retired assignment attribute")
			}
			previous = sqltype.Null(current.Type().BaseType().CompositeMember(index).Usage.Type)
			if !current.IsNull() {
				previous = current.CompositeField(index)
			}
		} else if t.Node.Slice {
			previous, err = sqltype.ArraySlice(ctx.FunctionContext(), current, bounds)
		} else {
			previous, err = sqltype.ArrayFetch(ctx.FunctionContext(), current, bounds)
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		rhs, err = t.Next.applyPath(ctx, previous, expression, rhs)
	} else if expression != nil {
		rhs, err = expression.Eval(ctx)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return t.applyBounds(ctx, current, rhs, bounds)
}

func (t *ArrayAssignmentTarget) applyBounds(ctx expr.Context, current, rhs sqltype.Value, bounds []sqltype.ArraySubscriptValue) (sqltype.Value, error) {
	var result sqltype.Value
	var err error
	if t.Attribute != 0 {
		shape := current.Type().BaseType()
		index, ok := shape.CompositeAttributeIndex(t.Attribute)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: retired assignment attribute")
		}
		fields := make([]sqltype.Value, shape.CompositeLen())
		for i := range fields {
			fields[i] = sqltype.Null(shape.CompositeMember(i).Usage.Type)
			if !current.IsNull() {
				fields[i] = current.CompositeField(i)
			}
		}
		fields[index] = rhs
		result, err = sqltype.OwnedCompositeValue(shape, fields)
		if err != nil {
			return sqltype.Value{}, err
		}
		return t.Reapply.Apply(ctx.FunctionContext(), result, ctx.FunctionInput(), ctx.FunctionOutput())
	}
	if t.Node.Slice {
		result, err = sqltype.ArraySetSlice(ctx.FunctionContext(), current, bounds, rhs)
	} else {
		result, err = sqltype.ArraySetElement(ctx.FunctionContext(), current, bounds, rhs)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return t.Reapply.Apply(ctx.FunctionContext(), result, ctx.FunctionInput(), ctx.FunctionOutput())
}

// Scalar lists scan their typed children directly; array inputs scan the owner's
// immutable flat payload. Neither route constructs an array per input row.
type ScalarArrayComparisonExpr struct {
	expr.Info
	Left, Array         expr.Expr
	Items               []expr.Expr
	Predicate           sqltype.ComparisonPredicate
	Operator, Namespace string
	All                 bool
}

func (n ScalarArrayComparisonExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	left, err := n.Left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	var array sqltype.Value
	count := len(n.Items)
	if n.Array != nil {
		array, err = n.Array.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if array.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
		count = array.ArrayLen()
	}
	unknown := false
	decisive := false
	for i := range count {
		if err := ctx.FunctionContext().Err(); err != nil {
			return sqltype.Value{}, err
		}
		var right sqltype.Value
		if n.Array != nil {
			right = array.ArrayElement(i)
		} else {
			right, err = n.Items[i].Eval(ctx)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		// A batch corresponds to ARRAY[...] input: every item is evaluated
		// even after reduction becomes decisive, without allocating the array.
		if decisive {
			continue
		}
		value, err := n.Predicate.Evaluate(ctx.FunctionContext(), left, right, ctx.FunctionInput())
		if err != nil {
			return sqltype.Value{}, err
		}
		truth, err := TruthOf(value)
		if err != nil {
			return sqltype.Value{}, err
		}
		if truth == Unknown {
			unknown = true
			continue
		}
		if (!n.All && truth == True) || (n.All && truth == False) {
			if n.Array != nil {
				return sqltype.Boolean(n.Usage.Type, !n.All), nil
			}
			decisive = true
		}
	}
	if decisive {
		return sqltype.Boolean(n.Usage.Type, !n.All), nil
	}
	if unknown {
		return sqltype.Null(n.Usage.Type), nil
	}
	return sqltype.Boolean(n.Usage.Type, n.All), nil
}

func (n ScalarArrayComparisonExpr) ElementType() *sqltype.Descriptor {
	if n.Array != nil {
		return n.Array.ResultUsage().Type.BaseType().Element()
	}
	return n.Items[0].ResultUsage().Type
}

// Constructors retain scalar children and their stored dependencies rather than
// replacing the expression with opaque array input text.
type ArrayExpr struct {
	expr.Info
	Elements         []expr.Expr
	Multidimensional bool
}

func (n ArrayExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	values := make([]sqltype.Value, len(n.Elements))
	for i, child := range n.Elements {
		if err := ctx.FunctionContext().Err(); err != nil {
			return sqltype.Value{}, err
		}
		v, err := child.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return sqltype.ConstructArray(ctx.FunctionContext(), n.Usage.Type, values, n.Multidimensional)
}

type BoundArraySubscript struct {
	Lower, Upper expr.Expr
	Slice        bool
}

type SubscriptExpr struct {
	expr.Info
	Base       expr.Expr
	Subscripts []BoundArraySubscript
	Slice      bool
}

func (n SubscriptExpr) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := BeginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.Base.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	var storage [6]sqltype.ArraySubscriptValue
	bounds, err := evaluateArrayBounds(ctx, n.Subscripts, n.Slice, storage[:len(n.Subscripts)])
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Slice {
		return sqltype.ArraySlice(ctx.FunctionContext(), value, bounds)
	}
	return sqltype.ArrayFetch(ctx.FunctionContext(), value, bounds)
}

// Executor ordering is upper bounds in dimension order, then lower bounds.
// Analysis and diagnostic positions still follow lexical occurrences.
func evaluateArrayBounds(ctx expr.Context, subs []BoundArraySubscript, slice bool, bounds []sqltype.ArraySubscriptValue) ([]sqltype.ArraySubscriptValue, error) {
	for i, s := range subs {
		bounds[i] = sqltype.ArraySubscriptValue{}
		if slice && !s.Slice {
			bounds[i].Lower, bounds[i].HasLower = 1, true
		}
	}
	for _, lower := range [2]bool{false, true} {
		for i, s := range subs {
			node := s.Upper
			if lower {
				node = s.Lower
			}
			if node == nil {
				continue
			}
			v, err := node.Eval(ctx)
			if err != nil {
				return nil, err
			}
			var number int64
			if !v.IsNull() {
				number, err = v.Integer()
				if err != nil {
					return nil, err
				}
			}
			if lower {
				bounds[i].Lower, bounds[i].HasLower, bounds[i].LowerNull = int32(number), true, v.IsNull()
			} else {
				bounds[i].Upper, bounds[i].HasUpper, bounds[i].UpperNull = int32(number), true, v.IsNull()
			}
		}
	}
	return bounds, nil
}
