package sqltype

// HeapRewriteRequired describes PostgreSQL's planned coercion, not the native
// representation work. Typmod support functions erase widening/no-op sizing;
// domain constraints and real conversion functions still require a heap pass.
func (p CoercionPlan) HeapRewriteRequired(zone *Zone) bool {
	if p.source.Type == nil || p.target.Type == nil {
		return true
	}
	if p.ChecksDomain() {
		return true
	}
	source, target := p.source, p.target
	switch p.method {
	case viaIO, jsonFormattedInput, jsonReturningOutput, arrayConversion:
		return true
	case datetimeConversion:
		a, b := source.Type.ID(), target.Type.ID()
		if (a == Timestamp && b == Timestamptz || a == Timestamptz && b == Timestamp) && !zone.TimestampConversionRewrites() {
			return temporalTypmodRewrites(source.Typmod, target.Typmod)
		}
		return true
	case identity:
		if source.Typmod == target.Typmod || target.Typmod < 0 {
			return false
		}
		switch target.Type.ID() {
		case VarChar:
			return source.Typmod < 0 || source.Typmod > target.Typmod
		case Numeric:
			if !numericTypmodValid(target.Typmod) {
				return false
			}
			if !numericTypmodValid(source.Typmod) {
				return true
			}
			sp, ss := numericParts(source.Typmod)
			tp, ts := numericParts(target.Typmod)
			return ss != ts || sp > tp
		case Time, Timetz, Timestamp, Timestamptz:
			return temporalTypmodRewrites(source.Typmod, target.Typmod)
		default:
			return true
		}
	case stringConversion:
		if (source.Type.ID() == Text || source.Type.ID() == VarChar) && (target.Type.ID() == Text || target.Type.ID() == VarChar) {
			return target.Typmod >= 0 && (source.Typmod < 0 || source.Typmod > target.Typmod)
		}
	}
	return p.FunctionExpression()
}

func temporalTypmodRewrites(old, next int32) bool {
	return next >= 0 && next < 6 && (old < 0 || next < old)
}
