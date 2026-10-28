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
		return p.source.Type.OutputSessionDependent() || p.target.Type.InputSessionDependent()
	}
	return false
}

// InputSessionDependent follows the input codec actually invoked by coercions.
// Domains delegate to base input here; domain_in itself is not invoked.
func (d *Descriptor) InputSessionDependent() bool {
	base := d.BaseType()
	if base.id == XML {
		return true
	}
	if base.IsArray() || base.IsEnum() || base.IsComposite() || base.rangeSubtype != nil || base.multirangeRange != nil {
		return true
	}
	switch base.ID() {
	case ACLItem, Money, Regclass, Regtype, Regrole, Regnamespace, Regcollation, Regoper, Regoperator, Regproc, Regprocedure, Regconfig, Regdictionary, Date, Time, Timestamp, Timestamptz, Timetz, Interval:
		return true
	default:
		return false
	}
}

// OutputSessionDependent follows the release output procedure's declared
// volatility, not whether an implementation happens to read an environment.
// Domain output delegates to its concrete base output procedure.
func (d *Descriptor) OutputSessionDependent() bool {
	base := d.BaseType()
	if base.IsArray() || base.IsEnum() || base.IsComposite() || base.rangeSubtype != nil || base.multirangeRange != nil {
		return true
	}
	switch base.ID() {
	case ACLItem, Money, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regcollation, Regnamespace, Regoper, Regoperator, Regconfig, Regdictionary, Date, Timestamp, Timestamptz, Interval, Float4, Float8:
		return true
	default:
		return false
	}
}
