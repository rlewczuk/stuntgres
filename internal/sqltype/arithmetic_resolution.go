package sqltype

// ArithmeticSignature describes a PostgreSQL candidate, including excluded
// providers. Resolution must not discard NUMERIC merely to select FLOAT8.
type ArithmeticSignature struct{ Left, Right, Result ID }

var arithmeticNumericSignatures = [...]ArithmeticSignature{
	{Int2, Int2, Int2}, {Int2, Int4, Int4}, {Int2, Int8, Int8},
	{Int4, Int2, Int4}, {Int4, Int4, Int4}, {Int4, Int8, Int8},
	{Int8, Int2, Int8}, {Int8, Int4, Int8}, {Int8, Int8, Int8},
	{Float4, Float4, Float4}, {Float4, Float8, Float8}, {Float8, Float4, Float8}, {Float8, Float8, Float8}, {Numeric, Numeric, Numeric},
}
var arithmeticPowerSignatures = [...]ArithmeticSignature{{Float8, Float8, Float8}, {Numeric, Numeric, Numeric}}
var arithmeticRemainderSignatures = [...]ArithmeticSignature{{Int2, Int2, Int2}, {Int4, Int4, Int4}, {Int8, Int8, Int8}, {Numeric, Numeric, Numeric}}

func ResolveArithmetic(operator ArithmeticOperator, left, right *Descriptor, unary bool) (ArithmeticSignature, error) {
	if operator > ArithmeticPower || unary && operator > ArithmeticSubtract {
		return ArithmeticSignature{}, failure("42883", "incompatible arithmetic operands")
	}
	if unary {
		if left == nil {
			if operator == ArithmeticAdd {
				return ArithmeticSignature{Left: Float8, Result: Float8}, nil
			}
			return ArithmeticSignature{}, failure("42725", "ambiguous arithmetic operands")
		}
		id := left.BaseType().ID()
		if equalityCategory(id) != 'N' || id == Oid {
			return ArithmeticSignature{}, failure("42883", "incompatible arithmetic operands")
		}
		return ArithmeticSignature{Left: id, Result: id}, nil
	}
	// PostgreSQL also has nonnumeric binary +/- candidates. With two unknown
	// arguments those categories cannot choose a unique numeric signature.
	if left == nil && right == nil && (operator == ArithmeticAdd || operator == ArithmeticSubtract) {
		return ArithmeticSignature{}, failure("42725", "ambiguous arithmetic operands")
	}
	signatures := arithmeticNumericSignatures[:]
	if operator == ArithmeticPower {
		signatures = arithmeticPowerSignatures[:]
	}
	if operator == ArithmeticRemainder {
		signatures = arithmeticRemainderSignatures[:]
	}
	return resolveBinaryArithmetic(signatures, left, right)
}

func resolveBinaryArithmetic(signatures []ArithmeticSignature, left, right *Descriptor) (ArithmeticSignature, error) {
	var l, r ID
	if left != nil {
		l = left.BaseType().ID()
	}
	if right != nil {
		r = right.BaseType().ID()
	}
	for _, s := range signatures {
		if left != nil && right != nil && s.Left == l && s.Right == r || left == nil && right != nil && s.Left == r && s.Right == r || right == nil && left != nil && s.Left == l && s.Right == l {
			return s, nil
		}
	}
	var active [14]bool
	count := 0
	for i, s := range signatures {
		if (left == nil || equalityImplicit(l, s.Left)) && (right == nil || equalityImplicit(r, s.Right)) {
			active[i] = true
			count++
		}
	}
	choose := func() (ArithmeticSignature, bool) {
		if count != 1 {
			return ArithmeticSignature{}, false
		}
		for i, ok := range active {
			if ok {
				return signatures[i], true
			}
		}
		return ArithmeticSignature{}, false
	}
	if s, ok := choose(); ok {
		return s, nil
	}
	for phase := range 2 {
		best := -1
		for i, s := range signatures {
			if !active[i] {
				continue
			}
			score := 0
			for j, id := range [...]ID{s.Left, s.Right} {
				source, known := l, left != nil
				if j == 1 {
					source, known = r, right != nil
				}
				if known && (id == source || phase == 1 && equalityCategory(id) == equalityCategory(source) && equalityPreferred(id)) {
					score++
				}
			}
			if score > best {
				best = score
			}
		}
		for i, s := range signatures {
			if !active[i] {
				continue
			}
			score := 0
			for j, id := range [...]ID{s.Left, s.Right} {
				source, known := l, left != nil
				if j == 1 {
					source, known = r, right != nil
				}
				if known && (id == source || phase == 1 && equalityCategory(id) == equalityCategory(source) && equalityPreferred(id)) {
					score++
				}
			}
			if score < best {
				active[i] = false
				count--
			}
		}
		if s, ok := choose(); ok {
			return s, nil
		}
	}
	for side := range 2 {
		if side == 0 && left != nil || side == 1 && right != nil {
			continue
		}
		category := byte(0)
		conflict := false
		preferred := false
		for i, s := range signatures {
			if !active[i] {
				continue
			}
			id := s.Left
			if side == 1 {
				id = s.Right
			}
			c := equalityCategory(id)
			if c == 'S' {
				category = 'S'
			} else if category == 0 {
				category = c
			} else if category != c && category != 'S' {
				conflict = true
			}
		}
		if category != 'S' && conflict {
			break
		}
		for i, s := range signatures {
			if !active[i] {
				continue
			}
			id := s.Left
			if side == 1 {
				id = s.Right
			}
			if equalityCategory(id) == category && equalityPreferred(id) {
				preferred = true
			}
		}
		for i, s := range signatures {
			if !active[i] {
				continue
			}
			id := s.Left
			if side == 1 {
				id = s.Right
			}
			if equalityCategory(id) != category || preferred && !equalityPreferred(id) {
				active[i] = false
				count--
			}
		}
		if s, ok := choose(); ok {
			return s, nil
		}
	}
	if (left == nil) != (right == nil) {
		known := l
		if left == nil {
			known = r
		}
		matches := 0
		var selected ArithmeticSignature
		for i, s := range signatures {
			if active[i] && equalityImplicit(known, s.Left) && equalityImplicit(known, s.Right) {
				selected = s
				matches++
			}
		}
		if matches == 1 {
			return selected, nil
		}
	}
	if count == 0 {
		return ArithmeticSignature{}, failure("42883", "incompatible arithmetic operands")
	}
	return ArithmeticSignature{}, failure("42725", "ambiguous arithmetic operands")
}
