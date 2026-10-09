package sqltype

func (p CoercionPlan) SessionDependent() bool {
	source, target := p.source.Type.id, p.target.Type.id
	if p.method == datetimeConversion {
		return source == Timestamptz || target == Timestamptz || target == Timetz
	}
	if p.method == viaIO {
		// CoerceViaIO invokes both the source output and target input procedure.
		// array_in and interval_in are STABLE even for immutable TEXT input.
		return p.source.Type.OutputSessionDependent() || p.target.Type.BaseType().IsArray() || target == Date || target == Time || target == Timestamp || target == Timestamptz || target == Timetz || target == Interval
	}
	return false
}

// OutputSessionDependent follows the release output procedure's declared
// volatility, not whether an implementation happens to read an environment.
// Domain output delegates to its concrete base output procedure.
func (d *Descriptor) OutputSessionDependent() bool {
	if d.BaseType().IsArray() {
		return true
	}
	switch d.BaseType().ID() {
	case Regclass, Date, Timestamp, Timestamptz, Interval:
		return true
	default:
		return false
	}
}
