package sqltype

// OperatorInputs is an ordered declared signature. Left zero denotes prefix.
type OperatorInputs struct{ Left, Right ID }

func operatorImplicitInput(source *Descriptor, target ID) bool {
	if source == nil {
		return true
	}
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
		return isRange(source.ID())
	case 4537, 4538:
		return isMultirange(source.ID())
	case 2249:
		return source.IsComposite() || source.IsDeferredRecord()
	}
	return equalityImplicit(source.ID(), target)
}

// ResolveOperator chooses an index, retaining the caller's catalog identity.
// Candidate collection owns search-path masking; this function owns §10.2 type
// selection. Unknown input is nil. Only direct implicit conversions are viable.
func ResolveOperator(candidates []OperatorInputs, left, right *Descriptor, unary bool) (int, error) {
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
		if (unary || operatorImplicitInput(left, s.Left)) && operatorImplicitInput(right, s.Right) {
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
		known := base[0]
		if left == nil {
			known = base[1]
		}
		chosen := -1
		for _, i := range active {
			s := candidates[i]
			if equalityImplicit(known, s.Left) && equalityImplicit(known, s.Right) {
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
