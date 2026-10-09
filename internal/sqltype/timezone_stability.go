package sqltype

func (p CoercionPlan) SessionDependent() bool {
	source, target := p.source.Type.id, p.target.Type.id
	if p.method == datetimeConversion {
		return source == Timestamptz || target == Timestamptz || target == Timetz
	}
	if p.method == viaIO {
		return source == Regclass || source == Date || source == Timestamp || source == Timestamptz || target == Date || target == Time || target == Timestamp || target == Timestamptz || target == Timetz
	}
	return false
}
