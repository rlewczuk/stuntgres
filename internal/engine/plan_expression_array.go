package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Array target plans retain only analysis state. Every bound and RHS is evaluated
// against the original execution frame, while writes compose into a private row.
type arrayAssignmentTarget struct {
	node        subscriptExpression
	container   sqltype.Usage
	destination sqltype.Usage
	reapply     sqltype.CoercionPlan
	name        string
	pos         parser.Position
	attribute   int32
	next        *arrayAssignmentTarget
}

func (t *arrayAssignmentTarget) column(original catalog.Column) catalog.Column {
	original.Usage = t.destination
	return original
}

func (t *arrayAssignmentTarget) apply(ctx expr.Context, current, rhs sqltype.Value) (sqltype.Value, error) {
	return t.applyPath(ctx, current, nil, rhs)
}

func (t *arrayAssignmentTarget) applyExpression(ctx expr.Context, current sqltype.Value, rhs expr.Expr) (sqltype.Value, error) {
	return t.applyPath(ctx, current, rhs, sqltype.Value{})
}

func (t *arrayAssignmentTarget) applyPath(ctx expr.Context, current sqltype.Value, expression expr.Expr, rhs sqltype.Value) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	var storage [6]sqltype.ArraySubscriptValue
	bounds, err := evaluateArrayBounds(ctx, t.node.subscripts, t.node.slice, storage[:len(t.node.subscripts)])
	if err != nil {
		return sqltype.Value{}, err
	}
	for _, bound := range bounds {
		if bound.LowerNull || bound.UpperNull {
			return sqltype.Value{}, sqlError("22004", "array subscript in assignment must not be null")
		}
	}
	if t.next != nil {
		var previous sqltype.Value
		if t.attribute != 0 {
			index, ok := current.Type().BaseType().CompositeAttributeIndex(t.attribute)
			if !ok {
				return sqltype.Value{}, fmt.Errorf("stuntgres: retired assignment attribute")
			}
			previous = sqltype.Null(current.Type().BaseType().CompositeMember(index).Usage.Type)
			if !current.IsNull() {
				previous = current.CompositeField(index)
			}
		} else if t.node.slice {
			previous, err = sqltype.ArraySlice(ctx.FunctionContext(), current, bounds)
		} else {
			previous, err = sqltype.ArrayFetch(ctx.FunctionContext(), current, bounds)
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		rhs, err = t.next.applyPath(ctx, previous, expression, rhs)
	} else if expression != nil {
		rhs, err = expression.Eval(ctx)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return t.applyBounds(ctx, current, rhs, bounds)
}

func (t *arrayAssignmentTarget) applyBounds(ctx expr.Context, current, rhs sqltype.Value, bounds []sqltype.ArraySubscriptValue) (sqltype.Value, error) {
	var result sqltype.Value
	var err error
	if t.attribute != 0 {
		shape := current.Type().BaseType()
		index, ok := shape.CompositeAttributeIndex(t.attribute)
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
		return t.reapply.Apply(ctx.FunctionContext(), result, ctx.FunctionInput(), ctx.FunctionOutput())
	}
	if t.node.slice {
		result, err = sqltype.ArraySetSlice(ctx.FunctionContext(), current, bounds, rhs)
	} else {
		result, err = sqltype.ArraySetElement(ctx.FunctionContext(), current, bounds, rhs)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return t.reapply.Apply(ctx.FunctionContext(), result, ctx.FunctionInput(), ctx.FunctionOutput())
}

// Scalar lists scan their typed children directly; array inputs scan the owner's
// immutable flat payload. Neither route constructs an array per input row.
type scalarArrayComparisonExpression struct {
	expr.Info
	left, array         expr.Expr
	items               []expr.Expr
	predicate           sqltype.ComparisonPredicate
	operator, namespace string
	all                 bool
}

func (n scalarArrayComparisonExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	left, err := n.left.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	var array sqltype.Value
	count := len(n.items)
	if n.array != nil {
		array, err = n.array.Eval(ctx)
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
		if n.array != nil {
			right = array.ArrayElement(i)
		} else {
			right, err = n.items[i].Eval(ctx)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		// A batch corresponds to ARRAY[...] input: every item is evaluated
		// even after reduction becomes decisive, without allocating the array.
		if decisive {
			continue
		}
		value, err := n.predicate.Evaluate(ctx.FunctionContext(), left, right, ctx.FunctionInput())
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
		if (!n.all && truth == True) || (n.all && truth == False) {
			if n.array != nil {
				return sqltype.Boolean(n.Usage.Type, !n.all), nil
			}
			decisive = true
		}
	}
	if decisive {
		return sqltype.Boolean(n.Usage.Type, !n.all), nil
	}
	if unknown {
		return sqltype.Null(n.Usage.Type), nil
	}
	return sqltype.Boolean(n.Usage.Type, n.all), nil
}

func (n scalarArrayComparisonExpression) elementType() *sqltype.Descriptor {
	if n.array != nil {
		return n.array.ResultUsage().Type.BaseType().Element()
	}
	return n.items[0].ResultUsage().Type
}

// Constructors retain scalar children and their stored dependencies rather than
// replacing the expression with opaque array input text.
type arrayExpression struct {
	expr.Info
	elements         []expr.Expr
	multidimensional bool
}

func (n arrayExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	values := make([]sqltype.Value, len(n.elements))
	for i, child := range n.elements {
		if err := ctx.FunctionContext().Err(); err != nil {
			return sqltype.Value{}, err
		}
		v, err := child.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	return sqltype.ConstructArray(ctx.FunctionContext(), n.Usage.Type, values, n.multidimensional)
}

type boundArraySubscript struct {
	lower, upper expr.Expr
	slice        bool
}

type subscriptExpression struct {
	expr.Info
	base       expr.Expr
	subscripts []boundArraySubscript
	slice      bool
}

func (n subscriptExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := beginEvaluation(ctx); err != nil {
		return sqltype.Value{}, err
	}
	value, err := n.base.Eval(ctx)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	var storage [6]sqltype.ArraySubscriptValue
	bounds, err := evaluateArrayBounds(ctx, n.subscripts, n.slice, storage[:len(n.subscripts)])
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.slice {
		return sqltype.ArraySlice(ctx.FunctionContext(), value, bounds)
	}
	return sqltype.ArrayFetch(ctx.FunctionContext(), value, bounds)
}

// Executor ordering is upper bounds in dimension order, then lower bounds.
// Analysis and diagnostic positions still follow lexical occurrences.
func evaluateArrayBounds(ctx expr.Context, subs []boundArraySubscript, slice bool, bounds []sqltype.ArraySubscriptValue) ([]sqltype.ArraySubscriptValue, error) {
	for i, s := range subs {
		bounds[i] = sqltype.ArraySubscriptValue{}
		if slice && !s.slice {
			bounds[i].Lower, bounds[i].HasLower = 1, true
		}
	}
	for _, lower := range [2]bool{false, true} {
		for i, s := range subs {
			node := s.upper
			if lower {
				node = s.lower
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
