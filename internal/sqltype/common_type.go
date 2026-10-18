package sqltype

// CommonTypeCategory is the descriptor category used by common-type selection.
// Operator best-match scoring has its own, distinct category policy.
func CommonTypeCategory(t *Descriptor) byte {
	if t.BaseType().IsArray() {
		return 'A'
	}
	switch t.BaseType().ID() {
	case Inet, Cidr:
		return 'I'
	case Bool:
		return 'B'
	case Oid, Int2, Int4, Int8, Numeric, Float4, Float8, Regconfig, Regdictionary:
		return 'N'
	case Text, BPChar, VarChar, Name:
		return 'S'
	case Date, Time, Timestamp, Timestamptz, Timetz:
		return 'D'
	case Interval:
		return 'T'
	case Bytea, UUID:
		return 'U'
	}
	return 0
}

// CommonTypePreferred reports the existing common-selection preference.
func CommonTypePreferred(t *Descriptor) bool {
	t = t.BaseType()
	return t.ID() == Inet || t.ID() == Oid || t.ID() == Text || t.ID() == Float8 || t.ID() == Bool
}

// CommonTypeCandidate folds two known, domain-normalized candidates. The bool
// reports category compatibility, not whether every input can be coerced to the
// result. Callers own domain preservation and subsequent conversion checks.
func CommonTypeCandidate(candidate, next *Descriptor) (*Descriptor, bool) {
	if candidate == next {
		return candidate, true
	}
	if CommonTypeCategory(candidate) != CommonTypeCategory(next) {
		return candidate, false
	}
	if !CommonTypePreferred(candidate) {
		_, forward := PlanCoercion(candidate.Usage(), next.Usage(), Implicit)
		_, reverse := PlanCoercion(next.Usage(), candidate.Usage(), Implicit)
		if forward == nil && reverse != nil {
			return next, true
		}
	}
	return candidate, true
}
