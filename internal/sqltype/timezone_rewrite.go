package sqltype

// TimestampConversionRewrites follows pg_get_timezone_offset: every historical
// rule must have zero UTC offset. A present-day winter offset is insufficient.
func (z *Zone) TimestampConversionRewrites() bool {
	if z == nil {
		return false
	}
	for _, rule := range z.types {
		if rule.east != 0 {
			return true
		}
	}
	if r := z.recurrence; r != nil && (r.standard.east != 0 || r.hasDST && r.daylight.east != 0) {
		return true
	}
	return false
}
