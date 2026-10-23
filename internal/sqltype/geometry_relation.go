package sqltype

import (
	"context"
	"fmt"
	"math"
)

// GeometryRelationOperator identifies only ordered Table 9.36 signatures.
// The existing polygon/point and circle/circle providers retain their ownership.
type GeometryRelationOperator uint8

var geometryRelations = [...]struct {
	name, routine       string
	left, right, result ID
}{
	{"@>", "box_contain_pt", Box, Point, Bool},
	{"<@", "poly_contained", Polygon, Polygon, Bool},
	{"@>", "poly_contain", Polygon, Polygon, Bool},
	{"&&", "poly_overlap", Polygon, Polygon, Bool},
	{"<@", "box_contained", Box, Box, Bool},
	{"@>", "box_contain", Box, Box, Bool},
	{"&&", "box_overlap", Box, Box, Bool},
	{"<@", "on_pb", Point, Box, Bool},
	{"<@", "on_ppath", Point, Path, Bool},
	{"<->", "point_distance", Point, Point, Float8},
	{"<->", "dist_bp", Box, Point, Float8},
	{"<->", "dist_pl", Point, Line, Float8},
	{"<->", "dist_ps", Point, Lseg, Float8},
	{"<->", "dist_pb", Point, Box, Float8},
	{"<->", "dist_sl", Lseg, Line, Float8},
	{"<->", "dist_sb", Lseg, Box, Float8},
	{"<->", "dist_ppath", Point, Path, Float8},
	{"<->", "box_distance", Box, Box, Float8},
	{"<->", "path_distance", Path, Path, Float8},
	{"<->", "line_distance", Line, Line, Float8},
	{"<->", "lseg_distance", Lseg, Lseg, Float8},
	{"<->", "poly_distance", Polygon, Polygon, Float8},
	{"@>", "path_contain_pt", Path, Point, Bool},
	{"<@", "pt_contained_poly", Point, Polygon, Bool},
	{"@>", "poly_contain_pt", Polygon, Point, Bool},
	{"<@", "pt_contained_circle", Point, Circle, Bool},
	{"@>", "circle_contain_pt", Circle, Point, Bool},
	{"<->", "dist_lp", Line, Point, Float8},
	{"<->", "dist_sp", Lseg, Point, Float8},
	{"<->", "dist_ls", Line, Lseg, Float8},
	{"<->", "dist_bs", Box, Lseg, Float8},
	{"<->", "dist_pathp", Path, Point, Float8},
	{"?#", "path_inter", Path, Path, Bool},
	{"?#", "box_overlap", Box, Box, Bool},
	{"#", "box_intersect", Box, Box, Box},
	{"<->", "dist_polyc", Polygon, Circle, Float8},
	{"<@", "circle_contained", Circle, Circle, Bool},
	{"@>", "circle_contain", Circle, Circle, Bool},
	{"&&", "circle_overlap", Circle, Circle, Bool},
	{"<->", "circle_distance", Circle, Circle, Float8},
	{"<->", "dist_pc", Point, Circle, Float8},
	{"<->", "dist_cpoly", Circle, Polygon, Float8},
	{"?#", "lseg_intersect", Lseg, Lseg, Bool},
	{"#", "lseg_interpt", Lseg, Lseg, Point},
	{"?#", "inter_sl", Lseg, Line, Bool},
	{"?#", "inter_sb", Lseg, Box, Bool},
	{"?#", "inter_lb", Line, Box, Bool},
	{"<@", "on_pl", Point, Line, Bool},
	{"<@", "on_ps", Point, Lseg, Bool},
	{"<@", "on_sl", Lseg, Line, Bool},
	{"<@", "on_sb", Lseg, Box, Bool},
	{"##", "close_pl", Point, Line, Point},
	{"##", "close_ps", Point, Lseg, Point},
	{"##", "close_pb", Point, Box, Point},
	{"##", "close_sb", Lseg, Box, Point},
	{"##", "close_ls", Line, Lseg, Point},
	{"##", "close_lseg", Lseg, Lseg, Point},
	{"?#", "line_intersect", Line, Line, Bool},
	{"#", "line_interpt", Line, Line, Point},
	{"<->", "dist_ppoly", Point, Polygon, Float8},
	{"<->", "dist_polyp", Polygon, Point, Float8},
	{"<->", "dist_cpoint", Circle, Point, Float8},
	{"<<", "box_left", Box, Box, Bool},
	{"&<", "box_overleft", Box, Box, Bool},
	{"&>", "box_overright", Box, Box, Bool},
	{">>", "box_right", Box, Box, Bool},
	{"<<|", "box_below", Box, Box, Bool},
	{"&<|", "box_overbelow", Box, Box, Bool},
	{"|&>", "box_overabove", Box, Box, Bool},
	{"|>>", "box_above", Box, Box, Bool},
	{"<^", "box_below_eq", Box, Box, Bool},
	{">^", "box_above_eq", Box, Box, Bool},
	{"<<", "poly_left", Polygon, Polygon, Bool},
	{"&<", "poly_overleft", Polygon, Polygon, Bool},
	{"&>", "poly_overright", Polygon, Polygon, Bool},
	{">>", "poly_right", Polygon, Polygon, Bool},
	{"<<|", "poly_below", Polygon, Polygon, Bool},
	{"&<|", "poly_overbelow", Polygon, Polygon, Bool},
	{"|&>", "poly_overabove", Polygon, Polygon, Bool},
	{"|>>", "poly_above", Polygon, Polygon, Bool},
	{"?-", "line_horizontal", 0, Line, Bool},
	{"?|", "line_vertical", 0, Line, Bool},
	{"?-", "lseg_horizontal", 0, Lseg, Bool},
	{"?|", "lseg_vertical", 0, Lseg, Bool},
	{"?||", "line_parallel", Line, Line, Bool},
	{"?-|", "line_perp", Line, Line, Bool},
	{"?||", "lseg_parallel", Lseg, Lseg, Bool},
	{"?-|", "lseg_perp", Lseg, Lseg, Bool},
}

func GeometryRelationKind(name string, left, right ID) (GeometryRelationOperator, bool) {
	for i, r := range geometryRelations {
		if r.name == name && r.left == left && r.right == right {
			return GeometryRelationOperator(i), true
		}
	}
	return 0, false
}
func (op GeometryRelationOperator) Name() string {
	if int(op) >= len(geometryRelations) {
		return ""
	}
	return geometryRelations[op].name
}
func (op GeometryRelationOperator) Routine() string {
	if op.Name() == "" {
		return ""
	}
	return geometryRelations[op].routine
}
func (op GeometryRelationOperator) Signature() (ID, ID) {
	if op.Name() == "" {
		return Unknown, Unknown
	}
	r := geometryRelations[op]
	return r.left, r.right
}
func (op GeometryRelationOperator) Arity() int {
	if op.Name() == "" {
		return 0
	}
	left, _ := op.Signature()
	if left == 0 {
		return 1
	}
	return 2
}
func (op GeometryRelationOperator) OperandID(index int) ID {
	left, right := op.Signature()
	if index < 0 || index >= op.Arity() {
		return Unknown
	}
	if op.Arity() == 1 || index == 1 {
		return right
	}
	return left
}
func (op GeometryRelationOperator) ResultID() ID {
	if op.Name() == "" {
		return Unknown
	}
	return geometryRelations[op].result
}
func (op GeometryRelationOperator) ExistingProvider() bool {
	l, r := op.Signature()
	return l == Circle && r == Circle || l == Polygon && r == Point && op.Name() == "@>" || l == Point && r == Polygon && op.Name() == "<@"
}

type GeometryRelationPlan struct {
	operation GeometryRelationOperator
	inputs    [2]*Descriptor
	result    *Descriptor
}

func BindGeometryRelation(op GeometryRelationOperator, result Usage, inputs [2]Usage) (GeometryRelationPlan, error) {
	if op.Name() == "" || result.Type == nil || result.Type.id != op.ResultID() || result != result.Type.Usage() {
		return GeometryRelationPlan{}, fmt.Errorf("invalid geometry relation result")
	}
	owner := geometryConversionOwner(inputs[0].Type)
	if owner == nil {
		return GeometryRelationPlan{}, fmt.Errorf("invalid geometry relation authority")
	}
	if result.Type.id == Bool {
		if result.Type != relationBooleanOwner(inputs[0].Type) {
			return GeometryRelationPlan{}, fmt.Errorf("foreign geometry relation result")
		}
	} else if geometryConversionOwner(result.Type) != owner {
		return GeometryRelationPlan{}, fmt.Errorf("foreign geometry relation result")
	}
	p := GeometryRelationPlan{operation: op, result: result.Type}
	for i := range op.Arity() {
		id := op.OperandID(i)
		u := inputs[i]
		if u.Type == nil || u.Type.id != id || u != u.Type.Usage() || geometryConversionOwner(u.Type) != owner {
			return GeometryRelationPlan{}, fmt.Errorf("invalid geometry relation operand")
		}
		p.inputs[i] = u.Type
	}
	if op.Arity() == 1 && inputs[1] != (Usage{}) {
		return GeometryRelationPlan{}, fmt.Errorf("extra geometry relation operand")
	}
	return p, nil
}
func relationBooleanOwner(d *Descriptor) *Descriptor {
	if d == nil {
		return nil
	}
	switch d.id {
	case Point:
		return d.pointBoolean
	case Circle:
		return d.circleBoolean
	case Polygon:
		return d.polygonBoolean
	case Box, Line, Lseg, Path:
		if d.geometryPoint != nil {
			return d.geometryPoint.pointBoolean
		}
	}
	return nil
}
func (p GeometryRelationPlan) Operation() GeometryRelationOperator { return p.operation }
func (p GeometryRelationPlan) Name() string                        { return p.operation.Name() }
func (p GeometryRelationPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || p.operation.Name() == "" {
		return Value{}, fmt.Errorf("invalid geometry relation plan")
	}
	count := p.operation.Arity()
	if count == 1 && values[1] != (Value{}) {
		return Value{}, fmt.Errorf("extra geometry relation value")
	}
	for i, v := range values[:count] {
		if v.typ != p.inputs[i] {
			return Value{}, fmt.Errorf("invalid geometry relation value")
		}
		if v.null {
			continue
		}
		switch v.typ.id {
		case Box, Lseg, Line:
			if v.geometry == nil {
				return Value{}, fmt.Errorf("missing geometry relation payload")
			}
		case Path:
			if v.path == nil || len(v.path.points) == 0 {
				return Value{}, fmt.Errorf("missing PATH payload")
			}
		case Polygon:
			if !v.validPolygon() {
				return Value{}, fmt.Errorf("missing polygon payload")
			}
		}
	}
	if values[0].null || count == 2 && values[1].null {
		return Null(p.result), nil
	}
	// Existing providers remain the SQL owners; support direct package plans too.
	if p.operation.ExistingProvider() {
		if values[0].typ.id == Circle {
			op, _ := CircleKind(p.Name())
			plan, err := BindCircle(op, p.result.Usage(), p.inputs[0].Usage(), p.inputs[1].Usage())
			if err != nil {
				return Value{}, err
			}
			return plan.Evaluate(ctx, values)
		}
		op, _ := PolygonKind(p.Name())
		plan, err := BindPolygon(op, p.result.Usage(), p.inputs[0].Usage(), p.inputs[1].Usage())
		if err != nil {
			return Value{}, err
		}
		return plan.Evaluate(ctx, values)
	}
	a, b := values[0], values[1]
	if p.Name() == "?-" || p.Name() == "?|" || p.Name() == "?||" || p.Name() == "?-|" {
		match, err := relationAlignment(a, b, p.Name())
		if err != nil {
			return Value{}, err
		}
		return Boolean(p.result, match), nil
	}
	if p.Name() == "<->" {
		n, valid, err := relationValueDistance(ctx, a, b)
		if err != nil {
			return Value{}, err
		}
		if !valid {
			return Null(p.result), nil
		}
		return Value{typ: p.result, floating: n}, nil
	}
	if p.Name() == "#" || p.Name() == "##" {
		if p.result.id == Box {
			x, y := a.geometry.coordinates, b.geometry.coordinates
			if !relationBoxOverlap(x, y) {
				return Null(p.result), nil
			}
			return Value{typ: p.result, geometry: &geometryPayload{coordinates: [4]float64{relationMin(x[0], y[0]), relationMin(x[1], y[1]), relationMax(x[2], y[2]), relationMax(x[3], y[3])}}}, nil
		}
		point, valid, err := relationValueClosest(a, b, p.Name() == "#")
		if err != nil {
			return Value{}, err
		}
		if !valid {
			return Null(p.result), nil
		}
		return pointValue(p.result, point[0], point[1]), nil
	}
	match, err := relationValuePredicate(ctx, a, b, p.Name())
	if err != nil {
		return Value{}, err
	}
	return Boolean(p.result, match), nil
}
func relationValuePoint(v Value) relationPoint {
	x, y := pointCoordinates(v)
	return relationPoint{x, y}
}
func relationValueSegment(v Value) relationSegment {
	c := v.geometry.coordinates
	return relationSegment{{c[0], c[1]}, {c[2], c[3]}}
}
func relationValueLine(v Value) relationLine {
	c := v.geometry.coordinates
	return relationLine{c[0], c[1], c[2]}
}
func relationValueClosest(a, b Value, intersection bool) (relationPoint, bool, error) {
	if intersection {
		if a.typ.id == Line {
			return relationLineIntersection(relationValueLine(a), relationValueLine(b))
		}
		return relationSegmentIntersection(relationValueSegment(a), relationValueSegment(b))
	}
	var q relationPoint
	var d float64
	var err error
	switch a.typ.id {
	case Point:
		point := relationValuePoint(a)
		switch b.typ.id {
		case Box:
			q, d, err = relationBoxPoint(b.geometry.coordinates, point)
		case Lseg:
			q, d, err = relationSegmentPoint(relationValueSegment(b), point)
		case Line:
			q, d, err = relationLinePoint(relationValueLine(b), point)
		}
	case Lseg:
		s := relationValueSegment(a)
		if b.typ.id == Box {
			q, d, err = relationBoxSegment(b.geometry.coordinates, s, true)
		} else {
			t := relationValueSegment(b)
			x, e := relationSlope(s[0], s[1], false)
			if e != nil {
				return q, false, e
			}
			y, e := relationSlope(t[0], t[1], false)
			if e != nil {
				return q, false, e
			}
			if x == y {
				return q, false, nil
			}
			q, d, err = relationSegmentClosest(t, s)
		}
	case Line:
		l, s := relationValueLine(a), relationValueSegment(b)
		x, e := relationLineSlope(l, false)
		if e != nil {
			return q, false, e
		}
		y, e := relationSlope(s[0], s[1], false)
		if e != nil {
			return q, false, e
		}
		if x == y {
			return q, false, nil
		}
		q, d, err = relationSegmentToLine(s, l)
	}
	return q, !math.IsNaN(d), err
}
func relationValueDistance(ctx context.Context, a, b Value) (float64, bool, error) {
	// Canonicalize only documented symmetric distance pairs, not signatures.
	if b.typ.id == Point && a.typ.id != Point || b.typ.id == Lseg && a.typ.id == Box || b.typ.id == Lseg && a.typ.id == Line || b.typ.id == Polygon && a.typ.id == Circle {
		a, b = b, a
	}
	var d float64
	var err error
	switch a.typ.id {
	case Point:
		q := relationValuePoint(a)
		switch b.typ.id {
		case Point:
			d, err = relationDistance(q, relationValuePoint(b))
		case Box:
			_, d, err = relationBoxPoint(b.geometry.coordinates, q)
		case Lseg:
			_, d, err = relationSegmentPoint(relationValueSegment(b), q)
		case Line:
			_, d, err = relationLinePoint(relationValueLine(b), q)
		case Path:
			d, err = relationPathPointDistance(ctx, b.path, q)
		case Polygon:
			d, err = relationPolygonPointDistance(ctx, b.polygon.points, q)
		case Circle:
			d, err = relationDistance(q, relationPoint{b.circle.x, b.circle.y})
			if err == nil {
				d, err = checkedFloatingBinary(ArithmeticSubtract, Float8, d, b.circle.radius)
				if d < 0 {
					d = 0
				}
			}
		}
	case Box:
		x, e := geometryMidpoint(a.geometry.coordinates)
		if e != nil {
			return 0, false, e
		}
		y, e := geometryMidpoint(b.geometry.coordinates)
		if e != nil {
			return 0, false, e
		}
		d, err = relationDistance(x, y)
	case Lseg:
		s := relationValueSegment(a)
		switch b.typ.id {
		case Lseg:
			_, d, err = relationSegmentClosest(s, relationValueSegment(b))
		case Line:
			_, d, err = relationSegmentToLine(s, relationValueLine(b))
		case Box:
			_, d, err = relationBoxSegment(b.geometry.coordinates, s, false)
		}
	case Line:
		d, err = relationLineDistance(relationValueLine(a), relationValueLine(b))
	case Path:
		return relationPaths(ctx, a.path, b.path, false)
	case Polygon:
		if b.typ.id == Polygon {
			d, err = relationPolygonDistance(ctx, a.polygon.points, b.polygon.points)
		} else {
			d, err = relationPolygonPointDistance(ctx, a.polygon.points, relationPoint{b.circle.x, b.circle.y})
			if err == nil {
				d, err = checkedFloatingBinary(ArithmeticSubtract, Float8, d, b.circle.radius)
				if d < 0 {
					d = 0
				}
			}
		}
	}
	return d, true, err
}
func relationValuePredicate(ctx context.Context, a, b Value, name string) (bool, error) {
	switch name {
	case "<<", ">>", "&<", "&>", "<<|", "|>>", "&<|", "|&>", "<^", ">^":
		return relationValuePosition(ctx, a, b, name)
	}
	if name == "@>" {
		a, b = b, a
		name = "<@"
	}
	if name == "<@" {
		switch a.typ.id {
		case Point:
			q := relationValuePoint(a)
			switch b.typ.id {
			case Box:
				return relationInBox(b.geometry.coordinates, q), nil
			case Lseg:
				return relationOnSegment(relationValueSegment(b), q)
			case Line:
				return relationOnLine(relationValueLine(b), q)
			case Path:
				return relationPathContains(ctx, b.path, q)
			case Circle:
				d, err := relationDistance(q, relationPoint{b.circle.x, b.circle.y})
				return d <= b.circle.radius, err
			}
		case Box:
			return relationBoxContains(b.geometry.coordinates, a.geometry.coordinates), nil
		case Lseg:
			s := relationValueSegment(a)
			if b.typ.id == Box {
				return relationInBox(b.geometry.coordinates, s[0]) && relationInBox(b.geometry.coordinates, s[1]), nil
			}
			hit, err := relationOnLine(relationValueLine(b), s[0])
			if err != nil || !hit {
				return hit, err
			}
			return relationOnLine(relationValueLine(b), s[1])
		case Polygon:
			return relationPolygonContains(ctx, b.polygon.points, a.polygon.points)
		}
	}
	switch a.typ.id {
	case Box:
		return relationBoxOverlap(a.geometry.coordinates, b.geometry.coordinates), nil
	case Polygon:
		return relationPolygonOverlap(ctx, a.polygon.points, b.polygon.points)
	case Lseg:
		s := relationValueSegment(a)
		switch b.typ.id {
		case Box:
			return relationBoxSegmentIntersects(b.geometry.coordinates, s)
		case Line:
			_, hit, err := relationSegmentLineIntersection(s, relationValueLine(b))
			return hit, err
		case Lseg:
			_, hit, err := relationSegmentIntersection(s, relationValueSegment(b))
			return hit, err
		}
	case Line:
		if b.typ.id == Line {
			_, hit, err := relationLineIntersection(relationValueLine(a), relationValueLine(b))
			return hit, err
		}
		edges := relationBoxEdges(b.geometry.coordinates)
		for _, i := range [4]int{0, 1, 3, 2} {
			_, hit, err := relationSegmentLineIntersection(edges[i], relationValueLine(a))
			if err != nil || hit {
				return hit, err
			}
		}
		return false, nil
	case Path:
		_, hit, err := relationPaths(ctx, a.path, b.path, true)
		return hit, err
	}
	return false, fmt.Errorf("invalid geometry relation predicate")
}
