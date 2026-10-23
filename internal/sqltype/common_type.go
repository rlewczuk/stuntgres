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

// CommonUsageMismatch retains diagnostic evidence without depending on SQL
// syntax or constructing an analyzer error.
type CommonUsageMismatch struct {
	Left, Right *Descriptor
	Index       int
}

// SelectCommonUsage is the shared domain/type/typmod fold. An absent Type is
// an unknown analysis operand, not a runtime NULL datum.
func SelectCommonUsage(count int, usage func(int) Usage, text Usage) (Usage, CommonUsageMismatch) {
	var domain *Descriptor
	allDomain := count > 0
	for i := range count {
		d := usage(i).Type
		if d == nil || !d.IsDomain() || domain != nil && domain != d {
			allDomain = false
			break
		}
		domain = d
	}
	if allDomain {
		return usage(0), CommonUsageMismatch{}
	}
	var candidate Usage
	typmod := int32(-1)
	sameMod := true
	for i := range count {
		u := usage(i)
		if u.Type == nil {
			sameMod = false
			continue
		}
		if u.Type.IsDomain() {
			u.Type = u.Type.BaseType()
			u.Typmod = -1
			sameMod = false
		}
		if candidate.Type == nil {
			candidate = u
			typmod = u.Typmod
			continue
		}
		if u.Type != candidate.Type || u.Typmod != typmod {
			sameMod = false
		}
		if u.Type == candidate.Type {
			continue
		}
		chosen, compatible := CommonTypeCandidate(candidate.Type, u.Type)
		if !compatible {
			return Usage{}, CommonUsageMismatch{Left: candidate.Type, Right: u.Type, Index: i}
		}
		if chosen == u.Type {
			candidate = u
		}
	}
	if candidate.Type == nil {
		return text, CommonUsageMismatch{}
	}
	if sameMod {
		candidate.Typmod = typmod
	} else {
		candidate.Typmod = -1
	}
	return candidate, CommonUsageMismatch{}
}
