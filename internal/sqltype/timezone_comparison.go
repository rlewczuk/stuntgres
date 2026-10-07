package sqltype

func dateTimestampType(id ID) bool { return id == Date || id == Timestamp || id == Timestamptz }
func compareDatetime(a, b Value, env InputEnvironment) int {
	if a.typ.id == Date && b.typ.id == Timestamp {
		return compareDateTimestamp(a.integer, b.integer)
	}
	if a.typ.id == Timestamp && b.typ.id == Date {
		return -compareDateTimestamp(b.integer, a.integer)
	}
	if a.integer == TimestampNegativeInfinity || b.integer == TimestampPositiveInfinity {
		if a.integer == b.integer {
			return 0
		}
		return -1
	}
	if a.integer == TimestampPositiveInfinity || b.integer == TimestampNegativeInfinity {
		if a.integer == b.integer {
			return 0
		}
		return 1
	}
	instant := func(v Value) (int64, int) {
		n := v.integer
		if v.typ.id == Date {
			if n < timestampMinimum/microsPerDay {
				return 0, -1
			}
			if n >= timestampEnd/microsPerDay {
				return 0, 1
			}
			n *= microsPerDay
		}
		if v.typ.id != Timestamptz {
			east := env.Zone.CivilOffset(floorDiv(n, 1000000) + 946684800)
			n -= int64(east) * 1000000
		}
		return n, 0
	}
	av, as := instant(a)
	bv, bs := instant(b)
	if as < bs {
		return -1
	}
	if as > bs {
		return 1
	}
	if av < bv {
		return -1
	}
	if av > bv {
		return 1
	}
	return 0
}
