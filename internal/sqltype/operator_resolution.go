package sqltype

// OperatorInputs is an ordered declared signature. Left zero denotes prefix.
type OperatorInputs struct{ Left, Right ID }

func operatorImplicitInputWithCoercion(source *Descriptor, target ID, can func(*Descriptor, ID) bool) bool {
	if source == nil {
		return true
	}
	original := source
	source = source.BaseType()
	switch target {
	case 2277, 5078:
		return source.IsArray()
	case 2283, 5077:
		return true
	case 2776, 5079:
		return !source.IsArray()
	case 3500:
		return source.IsEnum()
	case 3831, 5080:
		return source.IsRange()
	case 4537, 4538:
		return source.IsMultirange()
	case 2249:
		return source.IsComposite() || source.IsDeferredRecord()
	}
	if can != nil {
		return can(original, target)
	}
	return equalityImplicit(source.ID(), target)
}

// operatorImplicitInputs checks potential coercibility of a complete declared
// signature. Unknowns add no constraints; generic result instantiation remains
// the binder's responsibility.
func operatorImplicitInputs(left, right *Descriptor, declared OperatorInputs, unary bool) bool {
	return operatorImplicitInputsWithCoercion(left, right, declared, unary, nil)
}

func operatorImplicitInputsWithCoercion(left, right *Descriptor, declared OperatorInputs, unary bool, can func(*Descriptor, ID) bool) bool {
	sources := [2]*Descriptor{left, right}
	targets := [2]ID{declared.Left, declared.Right}
	var element, array, rangeType, multirange *Descriptor
	var compatible [2]*Descriptor
	count := 0
	for i, source := range sources {
		if unary && i == 0 || source == nil {
			continue
		}
		target := targets[i]
		if !operatorImplicitInputWithCoercion(source, target, can) {
			return false
		}
		base := source.BaseType()
		var leaf *Descriptor
		switch target {
		case 2283, 2776:
			leaf = source
		case 3500:
			// A domain over an enum is not itself an enum.
			if !source.IsEnum() {
				return false
			}
			leaf = source
		case 2277:
			if !operatorSameType(&array, base) {
				return false
			}
			leaf = base.Element()
			if leaf == nil {
				return false
			}
		case 3831:
			if !operatorSameType(&rangeType, base) || base.rangeSubtype == nil {
				return false
			}
			leaf = base.rangeSubtype
		case 4537:
			member := base.multirangeRange
			if member == nil || member.rangeSubtype == nil ||
				!operatorSameType(&multirange, base) || !operatorSameType(&rangeType, member) {
				return false
			}
			leaf = member.rangeSubtype
		case 5077, 5078:
			leaf = source
			if target == 5078 {
				leaf = base.Element()
				if leaf == nil {
					return false
				}
			}
			compatible[count] = leaf
			count++
			continue
		}
		if leaf != nil && !operatorSameType(&element, leaf) {
			return false
		}
	}
	if count < 2 || compatible[0] == compatible[1] {
		return true
	}
	// Identical domains were preserved above; differing leaves follow the
	// same base-domain common selection as ordinary expression binding.
	first, second := compatible[0].BaseType(), compatible[1].BaseType()
	chosen, ok := commonTypeCandidateWithCoercion(first, second, can)
	if !ok {
		return false
	}
	for _, source := range compatible {
		if source == chosen {
			continue
		}
		if can != nil {
			if !can(source, chosen.ID()) {
				return false
			}
			continue
		}
		if _, err := PlanCoercion(source.Usage(), chosen.Usage(), Implicit); err != nil {
			return false
		}
	}
	return true
}

func operatorSameType(known **Descriptor, actual *Descriptor) bool {
	if *known == nil {
		*known = actual
		return true
	}
	return (*known).ID() == actual.ID()
}

// ResolveOperator chooses an index, retaining the caller's catalog identity.
// Candidate collection owns search-path masking; this function owns §10.2 type
// selection. Unknown input is nil. Only direct implicit conversions are viable.
func ResolveOperator(candidates []OperatorInputs, left, right *Descriptor, unary bool) (int, error) {
	return ResolveOperatorWithCoercion(candidates, left, right, unary, nil)
}

// ResolveOperatorWithCoercion uses the same operator ranking and polymorphic
// identity/shape rules as ResolveOperator. The optional pure predicate replaces
// concrete implicit feasibility only; it must not bind, evaluate or invoke a
// conversion. Catalog-aware callers retain descriptor lookup and edge authority.
// A nil predicate selects the existing fixed providers.
func ResolveOperatorWithCoercion(candidates []OperatorInputs, left, right *Descriptor, unary bool, can func(source *Descriptor, target ID) bool) (int, error) {
	sources := [2]*Descriptor{left, right}
	if unary {
		sources = [2]*Descriptor{nil, right}
	}
	exact := func(ids [2]ID) int {
		for i, s := range candidates {
			if s.Left == ids[0] && s.Right == ids[1] {
				return i
			}
		}
		return -1
	}
	var declared, base [2]ID
	for i, t := range sources {
		if t != nil {
			declared[i] = t.ID()
			base[i] = t.BaseType().ID()
		}
	}
	if unary {
		if right != nil {
			if i := exact(declared); i >= 0 {
				return i, nil
			}
		}
	} else {
		ids := declared
		if left == nil && right != nil {
			ids[0] = ids[1]
		}
		if right == nil && left != nil {
			ids[1] = ids[0]
		}
		if left != nil || right != nil {
			if i := exact(ids); i >= 0 {
				return i, nil
			}
		}
		if (left == nil) != (right == nil) {
			known := left
			if known == nil {
				known = right
			}
			if known.IsDomain() {
				id := known.BaseType().ID()
				if i := exact([2]ID{id, id}); i >= 0 {
					return i, nil
				}
			}
		}
	}
	active := make([]int, 0, len(candidates))
	for i, s := range candidates {
		if unary != (s.Left == 0) {
			continue
		}
		if operatorImplicitInputsWithCoercion(left, right, s, unary, can) {
			active = append(active, i)
		}
	}
	if len(active) == 0 {
		return -1, failure("42883", "operator does not exist")
	}
	if len(active) == 1 {
		return active[0], nil
	}
	for phase := range 2 {
		score := func(i int) int {
			s := candidates[i]
			ids := [2]ID{s.Left, s.Right}
			n := 0
			for j, id := range ids {
				if unary && j == 0 || sources[j] == nil {
					continue
				}
				if id == base[j] || phase == 1 && equalityCategory(id) == equalityCategory(base[j]) && equalityPreferred(id) {
					n++
				}
			}
			return n
		}
		best := -1
		for _, i := range active {
			if n := score(i); n > best {
				best = n
			}
		}
		keep := active[:0]
		for _, i := range active {
			if score(i) == best {
				keep = append(keep, i)
			}
		}
		active = keep
		if len(active) == 1 {
			return active[0], nil
		}
	}
	// Derive every unknown position's requirements from the same candidates.
	// Applying one position early can select a candidate rejected by another.
	var categories [2]byte
	var preferred [2]bool
	categoriesResolved := true
	for side := range 2 {
		if unary && side == 0 || sources[side] != nil {
			continue
		}
		idAt := func(i int) ID {
			if side == 0 {
				return candidates[i].Left
			}
			return candidates[i].Right
		}
		category := byte(0)
		conflict := false
		for _, i := range active {
			c := equalityCategory(idAt(i))
			if c == 'S' {
				category = 'S'
			} else if category == 0 {
				category = c
			} else if category != c && category != 'S' {
				conflict = true
			}
		}
		if category != 'S' && conflict {
			categoriesResolved = false
			break
		}
		categories[side] = category
		for _, i := range active {
			if equalityCategory(idAt(i)) == category && equalityPreferred(idAt(i)) {
				preferred[side] = true
			}
		}
	}
	if categoriesResolved {
		keep := active[:0]
		for _, i := range active {
			ids := [2]ID{candidates[i].Left, candidates[i].Right}
			matches := true
			for side, id := range ids {
				if unary && side == 0 || sources[side] != nil {
					continue
				}
				if equalityCategory(id) != categories[side] || preferred[side] && !equalityPreferred(id) {
					matches = false
					break
				}
			}
			if matches {
				keep = append(keep, i)
			}
		}
		// If no candidate satisfies all positions, retain the prior set for
		// the final unknown-assume-known rule rather than discarding it.
		if len(keep) > 0 {
			active = keep
			if len(active) == 1 {
				return active[0], nil
			}
		}
	}
	if !unary && (left == nil) != (right == nil) {
		known := left
		if known == nil {
			known = right
		}
		known = known.BaseType()
		chosen := -1
		for _, i := range active {
			s := candidates[i]
			if operatorImplicitInputsWithCoercion(known, known, s, false, can) {
				if chosen >= 0 {
					return -1, failure("42725", "operator is not unique")
				}
				chosen = i
			}
		}
		if chosen >= 0 {
			return chosen, nil
		}
	}
	return -1, failure("42725", "operator is not unique")
}
