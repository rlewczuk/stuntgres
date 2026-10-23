package sqltype

// RequiresCircleConstructorReplay identifies datums that ordinary circle text
// input cannot reconstruct, including leaves of already-supported containers.
// Domains retain their own identity; callers must replay their original cast.
func (v Value) RequiresCircleConstructorReplay() bool {
	if v.typ == nil || v.null {
		return false
	}
	d := v.typ.BaseType()
	if d.id == Circle {
		return v.circle.radius < 0
	}
	if d.IsComposite() {
		for i := range v.CompositeLen() {
			if v.CompositeField(i).RequiresCircleConstructorReplay() {
				return true
			}
		}
	}
	if d.IsArray() {
		for i := range v.ArrayLen() {
			if v.ArrayElement(i).RequiresCircleConstructorReplay() {
				return true
			}
		}
	}
	return false
}

func circleConstructorLiteral(v circleDatum) string {
	// Use exact coordinate spelling, not environment-rounded display. These
	// components cannot contain SQL quoting metacharacters.
	x := pathCoordinateOutput(v.x, OutputEnvironment{}, true)
	y := pathCoordinateOutput(v.y, OutputEnvironment{}, true)
	r := pathCoordinateOutput(v.radius, OutputEnvironment{}, true)
	return "pg_catalog.circle('(" + x + "," + y + ")'::pg_catalog.point,'" + r + "'::pg_catalog.float8)"
}
