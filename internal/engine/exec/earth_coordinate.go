package exec

import (
	"fmt"
	"math"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

// EarthCoordinateBody consumes the actual selected SQL declaration after its
// outer gate. The one expanded NULL-center route is caller-owned, not an entry
// into the STRICT earth_box wrapper. No evaluator survives this invocation.
func (c *Context) EarthCoordinateBody(n functions.EarthCoordinate, args []sqltype.Value, expanded bool) (sqltype.Value, error) {
	outer, err := ExpressionRoutineDescriptor(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if outer == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing earth declaration")
	}
	var body Context
	if expanded {
		if n.Plan.Operation() != sqltype.EarthBox || len(args) != 2 || !args[0].IsNull() || args[1].IsNull() {
			return sqltype.Value{}, fmt.Errorf("stuntgres: invalid expanded earth declaration")
		}
		body, err = BeginExpandedNativeDeclaration(c, outer, n.Info)
	} else {
		body, err = BeginNativeBody(c, outer, n.Info)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return (earthNativeBody{c: &body, outer: outer, source: n.Info, plan: n.Plan}).evaluate(args)
}

type earthNativeBody struct {
	c      *Context
	outer  *catalog.Procedure
	source expr.Info
	plan   sqltype.EarthCoordinatePlan
}

// Only immutable typed member occurrences select dynamic children. The nth
// coordinate is syntactic preorder (including repeated/dead CASE branches),
// never a lookup by a current SQL spelling or a dependency set.
func (b earthNativeBody) call(extension string, slot, nth int) (*catalog.Procedure, expr.Info, error) {
	recipe, ok := proc.RoutineDeclarationRecipe(b.outer.DeclarationBody)
	if !ok {
		return nil, expr.Info{}, fmt.Errorf("stuntgres: missing earth declaration recipe")
	}
	for ordinal := range recipe.CallCount() {
		_, part := recipe.CallPart(ordinal)
		if part.Kind != proc.DeclarationExtensionMember || part.Extension != extension || slot >= 0 && part.Slot != slot {
			continue
		}
		if nth == 0 {
			return NativeDeclarationCall(b.c, b.outer, b.source, ordinal)
		}
		nth--
	}
	return nil, expr.Info{}, fmt.Errorf("stuntgres: missing earth declaration member occurrence")
}

func (b earthNativeBody) earth(nth int) (sqltype.Value, error) {
	return b.coordinate(sqltype.EarthRadiusOperation, nth, nil)
}

func (b earthNativeBody) coordinate(op sqltype.EarthOperation, nth int, args []sqltype.Value) (value sqltype.Value, err error) {
	slot, ok := extension.EarthOperationSlot(op)
	if !ok {
		return value, fmt.Errorf("stuntgres: invalid earth child operation")
	}
	child, info, err := b.call("", slot, nth)
	if err != nil {
		return value, err
	}
	p, err := sqltype.BindEarthCoordinate(op, b.plan.Earth(), b.plan.Float())
	if err != nil {
		return value, err
	}
	for _, v := range args {
		if v.IsNull() {
			return sqltype.Null(p.Result()), nil
		}
	}
	token, err := info.EnterRoutine(b.c, args)
	if err != nil {
		return value, err
	}
	defer token.Leave()
	defer func() {
		if err != nil {
			err = nativeDeclarationStatementContext(b.c, child, err)
		}
	}()
	body, err := BeginNativeBody(b.c, child, info)
	if err != nil {
		return value, err
	}
	return (earthNativeBody{c: &body, outer: child, source: info, plan: p}).evaluate(args)
}

func (b earthNativeBody) binary(op sqltype.ArithmeticOperator, a, z sqltype.Value) (sqltype.Value, error) {
	p, err := sqltype.BindArithmetic(op, b.plan.Float(), b.plan.Float(), b.plan.Float())
	if err != nil {
		return sqltype.Value{}, err
	}
	return p.Binary(b.c.Context, a, z, b.c.FunctionInput())
}
func (b earthNativeBody) number(n float64) (sqltype.Value, error) {
	return sqltype.Float(b.plan.Float(), n)
}
func (b earthNativeBody) math(name string, args ...sqltype.Value) (sqltype.Value, error) {
	var usages [2]sqltype.Usage
	for i, v := range args {
		usages[i] = v.Type().Usage()
	}
	p, err := sqltype.BindMath(name, b.plan.Float().Usage(), usages[:len(args)]...)
	if err != nil {
		return sqltype.Value{}, err
	}
	return p.Evaluate(b.c.Context, args, b.c.FunctionInput())
}
func (b earthNativeBody) twice(nth int) (sqltype.Value, error) {
	radius, err := b.earth(nth)
	if err != nil {
		return sqltype.Value{}, err
	}
	two, _ := b.number(2)
	return b.binary(sqltype.ArithmeticMultiply, two, radius)
}

func (b earthNativeBody) evaluate(args []sqltype.Value) (sqltype.Value, error) {
	if err := b.c.Context.Err(); err != nil {
		return sqltype.Value{}, err
	}
	switch b.plan.Operation() {
	case sqltype.EarthRadiusOperation:
		return b.number(sqltype.EarthRadius)
	case sqltype.EarthSecToGC, sqltype.EarthGCToSec:
		return b.convert(args[0])
	case sqltype.EarthLL:
		return b.ll(args)
	case sqltype.EarthLatitude:
		for branch := range 3 {
			coordinate, err := b.coord(args[0], 3, branch)
			if err != nil {
				return sqltype.Value{}, err
			}
			radius, err := b.earth(branch)
			if err != nil {
				return sqltype.Value{}, err
			}
			ratio, err := b.binary(sqltype.ArithmeticDivide, coordinate, radius)
			if err != nil {
				return sqltype.Value{}, err
			}
			n, err := ratio.Float()
			if err != nil {
				return sqltype.Value{}, err
			}
			if branch == 0 {
				if n < -1 {
					return b.number(-90)
				}
				continue
			}
			if branch == 1 {
				if math.IsNaN(n) || n > 1 {
					return b.number(90)
				}
				continue
			}
			angle, err := b.math("asin", ratio)
			if err != nil {
				return sqltype.Value{}, err
			}
			return b.math("degrees", angle)
		}
	case sqltype.EarthLongitude:
		y, err := b.coord(args[0], 2, 0)
		if err != nil {
			return sqltype.Value{}, err
		}
		x, err := b.coord(args[0], 1, 1)
		if err != nil {
			return sqltype.Value{}, err
		}
		angle, err := b.math("atan2", y, x)
		if err != nil {
			return sqltype.Value{}, err
		}
		return b.math("degrees", angle)
	case sqltype.EarthDistance:
		a, err := args[0].BaseValue()
		if err != nil {
			return sqltype.Value{}, err
		}
		z, err := args[1].BaseValue()
		if err != nil {
			return sqltype.Value{}, err
		}
		_, info, err := b.call("cube", -1, 0)
		if err != nil {
			return sqltype.Value{}, err
		}
		values := [2]sqltype.Value{a, z}
		token, err := info.EnterRoutine(b.c, values[:])
		if err != nil {
			return sqltype.Value{}, err
		}
		p, err := sqltype.BindCubeSpatial(sqltype.CubeDistance, b.plan.Float().Usage(), a.Type().Usage(), z.Type().Usage())
		var chord sqltype.Value
		if err == nil {
			chord, err = p.Evaluate(b.c.Context, values)
		}
		token.Leave()
		if err != nil {
			return sqltype.Value{}, err
		}
		return b.coordinate(sqltype.EarthSecToGC, 0, []sqltype.Value{chord})
	case sqltype.EarthBox:
		chord, err := b.coordinate(sqltype.EarthGCToSec, 0, args[1:])
		if err != nil {
			return sqltype.Value{}, err
		}
		// In the caller-expanded route the demanded conversion really ran, but
		// the cube_enlarge STRICT gate still suppresses its actual FHET/kernel.
		if args[0].IsNull() {
			return sqltype.Null(b.plan.Result()), nil
		}
		center, err := args[0].BaseValue()
		if err != nil {
			return sqltype.Value{}, err
		}
		integer, ok := b.c.Catalog.LookupType(sqltype.Int4)
		if !ok {
			return sqltype.Value{}, fmt.Errorf("stuntgres: missing earth dimension type")
		}
		dimension, err := sqltype.Integer(integer, 3)
		if err != nil {
			return sqltype.Value{}, err
		}
		values := [3]sqltype.Value{center, chord, dimension}
		_, info, err := b.call("cube", -1, 0)
		if err != nil {
			return sqltype.Value{}, err
		}
		token, err := info.EnterRoutine(b.c, values[:])
		if err != nil {
			return sqltype.Value{}, err
		}
		defer token.Leave()
		return sqltype.TransformCube(b.c.Context, center.Type(), sqltype.CubeEnlarge, values[:])
	}
	return sqltype.Value{}, fmt.Errorf("stuntgres: invalid earth native operation")
}

func (b earthNativeBody) convert(distance sqltype.Value) (sqltype.Value, error) {
	d, err := distance.Float()
	if err != nil {
		return sqltype.Value{}, err
	}
	if d < 0 {
		return b.number(0)
	}
	var denominator sqltype.Value
	if b.plan.Operation() == sqltype.EarthGCToSec {
		denominator, err = b.earth(0)
	} else {
		denominator, err = b.twice(0)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	ratio, err := b.binary(sqltype.ArithmeticDivide, distance, denominator)
	if err != nil {
		return sqltype.Value{}, err
	}
	q, err := ratio.Float()
	if err != nil {
		return sqltype.Value{}, err
	}
	limit := float64(1)
	if b.plan.Operation() == sqltype.EarthGCToSec {
		limit = math.Pi
	}
	if math.IsNaN(q) || q > limit {
		if b.plan.Operation() == sqltype.EarthGCToSec {
			return b.twice(1)
		}
		pi, err := b.math("pi")
		if err != nil {
			return sqltype.Value{}, err
		}
		radius, err := b.earth(1)
		if err != nil {
			return sqltype.Value{}, err
		}
		return b.binary(sqltype.ArithmeticMultiply, pi, radius)
	}
	// ELSE has two distinct earth() occurrences. They are not shared even
	// though the default numeric result is equal.
	twice, err := b.twice(2)
	if err != nil {
		return sqltype.Value{}, err
	}
	divisor, err := b.twice(3)
	if err != nil {
		return sqltype.Value{}, err
	}
	ratio, err = b.binary(sqltype.ArithmeticDivide, distance, divisor)
	if err != nil {
		return sqltype.Value{}, err
	}
	trig := "asin"
	if b.plan.Operation() == sqltype.EarthGCToSec {
		trig = "sin"
	}
	angle, err := b.math(trig, ratio)
	if err != nil {
		return sqltype.Value{}, err
	}
	return b.binary(sqltype.ArithmeticMultiply, twice, angle)
}

func (b earthNativeBody) coord(value sqltype.Value, index int32, nth int) (sqltype.Value, error) {
	base, err := value.BaseValue()
	if err != nil {
		return sqltype.Value{}, err
	}
	integer, ok := b.c.Catalog.LookupType(sqltype.Int4)
	if !ok {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing earth coordinate type")
	}
	position, err := sqltype.Integer(integer, int64(index))
	if err != nil {
		return sqltype.Value{}, err
	}
	args := [2]sqltype.Value{base, position}
	_, info, err := b.call("cube", 26, nth)
	if err != nil {
		return sqltype.Value{}, err
	}
	token, err := info.EnterRoutine(b.c, args[:])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer token.Leave()
	n, err := sqltype.CubeCoordinate(b.c.Context, base.Type(), base, sqltype.CubeLowerCoordinate, index)
	if err != nil {
		return sqltype.Value{}, err
	}
	return b.number(n)
}

func (b earthNativeBody) ll(args []sqltype.Value) (sqltype.Value, error) {
	var point sqltype.Value
	for axis := range 3 {
		// Keep the released earth*cos(radians(lat))*trig(radians(lon)) order.
		radius, err := b.earth(axis)
		if err != nil {
			return sqltype.Value{}, err
		}
		latitude, err := b.math("radians", args[0])
		if err != nil {
			return sqltype.Value{}, err
		}
		trig := "cos"
		if axis == 2 {
			trig = "sin"
		}
		latitude, err = b.math(trig, latitude)
		if err != nil {
			return sqltype.Value{}, err
		}
		coordinate, err := b.binary(sqltype.ArithmeticMultiply, radius, latitude)
		if err != nil {
			return sqltype.Value{}, err
		}
		if axis != 2 {
			longitude, err := b.math("radians", args[1])
			if err != nil {
				return sqltype.Value{}, err
			}
			trig = "cos"
			if axis == 1 {
				trig = "sin"
			}
			longitude, err = b.math(trig, longitude)
			if err != nil {
				return sqltype.Value{}, err
			}
			coordinate, err = b.binary(sqltype.ArithmeticMultiply, coordinate, longitude)
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		slot, nth, operation := 18, 0, sqltype.CubeScalar
		var values [2]sqltype.Value
		values[0] = coordinate
		count := 1
		if axis != 0 {
			slot, nth, operation = 22, 2-axis, sqltype.CubeExtend
			values = [2]sqltype.Value{point, coordinate}
			count = 2
		}
		_, info, err := b.call("cube", slot, nth)
		if err != nil {
			return sqltype.Value{}, err
		}
		token, err := info.EnterRoutine(b.c, values[:count])
		if err != nil {
			return sqltype.Value{}, err
		}
		point, err = sqltype.ConstructCube(b.c.Context, b.plan.Earth().DomainBase().Type, operation, values[:count])
		token.Leave()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	if err := b.plan.Earth().CheckDomainInput(b.c.Context, point, b.c.FunctionInput()); err != nil {
		return sqltype.Value{}, err
	}
	return b.plan.Earth().Wrap(point)
}

// Native source adapters retain original identities, not diagnostic spellings.
// Resolve presentation only on failure, against the same current invocation
// authority that admitted the original declaration's lifetime.
func nativeDeclarationStatementContext(c *Context, outer *catalog.Procedure, err error) error {
	if err == nil {
		return nil
	}
	auth, _, failure := RoutineInvocationAuthority(c)
	if failure != nil {
		return failure
	}
	current, failure := ValidateStoredRoutineDescriptor(auth.Catalog, outer)
	if failure != nil {
		return failure
	}
	return SQLRoutineStatementContext(err, current.Name(), 1)
}
