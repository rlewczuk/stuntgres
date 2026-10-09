package sqltype

func (p CoercionPlan) SessionDependent() bool {
	source, target := p.source.Type.id, p.target.Type.id
	if p.method == datetimeConversion {
		return source == Timestamptz || target == Timestamptz || target == Timetz
	}
	if p.method == viaIO {
		return p.source.Type.OutputSessionDependent() || target == Date || target == Time || target == Timestamp || target == Timestamptz || target == Timetz
	}
	return false
}

// OutputSessionDependent follows the release output procedure's declared
// volatility, not whether an implementation happens to read an environment.
// Domain output delegates to its concrete base output procedure.
func (d *Descriptor) OutputSessionDependent() bool {
	switch d.BaseType().ID() {
	case Regclass, Date, Timestamp, Timestamptz, Interval, NameArray:
		return true
	default:
		return false
	}
}
