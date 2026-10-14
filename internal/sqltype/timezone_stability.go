package sqltype

func (p CoercionPlan) SessionDependent() bool {
	if p.element != nil {
		return p.element.SessionDependent()
	}
	source, target := p.source.Type.id, p.target.Type.id
	if p.method == moneyConversion {
		return true
	}
	if p.method == datetimeConversion {
		return source == Timestamptz || target == Timestamptz || target == Timetz
	}
	if p.method == viaIO {
		// CoerceViaIO invokes both the source output and target input procedure.
		// array_in and interval_in are STABLE even for immutable TEXT input.
		return p.source.Type.OutputSessionDependent() || p.target.Type.BaseType().IsArray() || target == ACLItem || target == Money || target == Regclass || target == Regtype || target == Regrole || IsOperatorAlias(target) || IsRoutineAlias(target) || IsRegObjectAlias(target) || target == Date || target == Time || target == Timestamp || target == Timestamptz || target == Timetz || target == Interval
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
	case ACLItem, Money, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Date, Timestamp, Timestamptz, Interval, Float4, Float8:
		return true
	default:
		return false
	}
}
