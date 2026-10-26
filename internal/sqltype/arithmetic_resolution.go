package sqltype

// ArithmeticSignature describes a PostgreSQL candidate. Resolution retains
// NUMERIC candidates rather than routing exact decimal operands through FLOAT8.
type ArithmeticSignature struct{ Left, Right, Result ID }

var arithmeticNumericSignatures = [...]ArithmeticSignature{
	{Int2, Int2, Int2}, {Int2, Int4, Int4}, {Int2, Int8, Int8},
	{Int4, Int2, Int4}, {Int4, Int4, Int4}, {Int4, Int8, Int8},
	{Int8, Int2, Int8}, {Int8, Int4, Int8}, {Int8, Int8, Int8},
	{Float4, Float4, Float4}, {Float4, Float8, Float8}, {Float8, Float4, Float8}, {Float8, Float8, Float8}, {Numeric, Numeric, Numeric},
}
var arithmeticPowerSignatures = [...]ArithmeticSignature{{Float8, Float8, Float8}, {Numeric, Numeric, Numeric}}
var arithmeticRemainderSignatures = [...]ArithmeticSignature{{Int2, Int2, Int2}, {Int4, Int4, Int4}, {Int8, Int8, Int8}, {Numeric, Numeric, Numeric}}

// Combined static candidate sets also participate in unknown-operand inference.
var arithmeticAddSignatures = [...]ArithmeticSignature{
	arithmeticNumericSignatures[0], arithmeticNumericSignatures[1], arithmeticNumericSignatures[2],
	arithmeticNumericSignatures[3], arithmeticNumericSignatures[4], arithmeticNumericSignatures[5],
	arithmeticNumericSignatures[6], arithmeticNumericSignatures[7], arithmeticNumericSignatures[8],
	arithmeticNumericSignatures[9], arithmeticNumericSignatures[10], arithmeticNumericSignatures[11],
	arithmeticNumericSignatures[12], arithmeticNumericSignatures[13],
	{Money, Money, Money},
	{PgLSN, Numeric, PgLSN}, {Numeric, PgLSN, PgLSN},
	{Date, Int4, Date}, {Int4, Date, Date},
	{Date, Time, Timestamp}, {Time, Date, Timestamp},
	{Date, Timetz, Timestamptz}, {Timetz, Date, Timestamptz},
	{Date, Interval, Timestamp}, {Interval, Date, Timestamp},
	{Timestamp, Interval, Timestamp}, {Interval, Timestamp, Timestamp},
	{Timestamptz, Interval, Timestamptz}, {Interval, Timestamptz, Timestamptz},
	{Time, Interval, Time}, {Interval, Time, Time},
	{Timetz, Interval, Timetz}, {Interval, Timetz, Timetz},
	{Interval, Interval, Interval},
	{Inet, Int8, Inet}, {Int8, Inet, Inet},
	{Point, Point, Point}, {Box, Point, Box}, {Path, Point, Path}, {Circle, Point, Circle}, {Path, Path, Path},
}
var arithmeticSubtractSignatures = [...]ArithmeticSignature{
	arithmeticNumericSignatures[0], arithmeticNumericSignatures[1], arithmeticNumericSignatures[2],
	arithmeticNumericSignatures[3], arithmeticNumericSignatures[4], arithmeticNumericSignatures[5],
	arithmeticNumericSignatures[6], arithmeticNumericSignatures[7], arithmeticNumericSignatures[8],
	arithmeticNumericSignatures[9], arithmeticNumericSignatures[10], arithmeticNumericSignatures[11],
	arithmeticNumericSignatures[12], arithmeticNumericSignatures[13],
	{Money, Money, Money},
	{PgLSN, PgLSN, Numeric}, {PgLSN, Numeric, PgLSN},
	{Date, Int4, Date}, {Date, Date, Int4}, {Date, Interval, Timestamp},
	{Timestamp, Interval, Timestamp}, {Timestamptz, Interval, Timestamptz},
	{Timestamp, Timestamp, Interval}, {Timestamptz, Timestamptz, Interval},
	{Time, Interval, Time}, {Timetz, Interval, Timetz}, {Time, Time, Interval},
	{Interval, Interval, Interval},
	{Inet, Int8, Inet}, {Inet, Inet, Int8},
	{Point, Point, Point}, {Box, Point, Box}, {Path, Point, Path}, {Circle, Point, Circle},
}
var arithmeticMultiplySignatures = [...]ArithmeticSignature{
	arithmeticNumericSignatures[0], arithmeticNumericSignatures[1], arithmeticNumericSignatures[2],
	arithmeticNumericSignatures[3], arithmeticNumericSignatures[4], arithmeticNumericSignatures[5],
	arithmeticNumericSignatures[6], arithmeticNumericSignatures[7], arithmeticNumericSignatures[8],
	arithmeticNumericSignatures[9], arithmeticNumericSignatures[10], arithmeticNumericSignatures[11],
	arithmeticNumericSignatures[12], arithmeticNumericSignatures[13],
	{Money, Int2, Money}, {Money, Int4, Money}, {Money, Int8, Money}, {Money, Float4, Money}, {Money, Float8, Money},
	{Int2, Money, Money}, {Int4, Money, Money}, {Int8, Money, Money}, {Float4, Money, Money}, {Float8, Money, Money},
	{Interval, Float8, Interval}, {Float8, Interval, Interval},
	{Point, Point, Point}, {Box, Point, Box}, {Path, Point, Path}, {Circle, Point, Circle},
}
var arithmeticDivideSignatures = [...]ArithmeticSignature{
	arithmeticNumericSignatures[0], arithmeticNumericSignatures[1], arithmeticNumericSignatures[2],
	arithmeticNumericSignatures[3], arithmeticNumericSignatures[4], arithmeticNumericSignatures[5],
	arithmeticNumericSignatures[6], arithmeticNumericSignatures[7], arithmeticNumericSignatures[8],
	arithmeticNumericSignatures[9], arithmeticNumericSignatures[10], arithmeticNumericSignatures[11],
	arithmeticNumericSignatures[12], arithmeticNumericSignatures[13],
	{Money, Money, Float8},
	{Money, Int2, Money}, {Money, Int4, Money}, {Money, Int8, Money}, {Money, Float4, Money}, {Money, Float8, Money},
	{Interval, Float8, Interval},
	{Point, Point, Point}, {Box, Point, Box}, {Path, Point, Path}, {Circle, Point, Circle},
}

func arithmeticCandidates(operator ArithmeticOperator) []ArithmeticSignature {
	switch operator {
	case ArithmeticAdd:
		return arithmeticAddSignatures[:]
	case ArithmeticSubtract:
		return arithmeticSubtractSignatures[:]
	case ArithmeticMultiply:
		return arithmeticMultiplySignatures[:]
	case ArithmeticDivide:
		return arithmeticDivideSignatures[:]
	case ArithmeticRemainder:
		return arithmeticRemainderSignatures[:]
	case ArithmeticPower:
		return arithmeticPowerSignatures[:]
	}
	return nil
}

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
		if (equalityCategory(id) != 'N' || id == Oid || id == Money) && !(operator == ArithmeticSubtract && id == Interval) {
			return ArithmeticSignature{}, failure("42883", "incompatible arithmetic operands")
		}
		return ArithmeticSignature{Left: id, Result: id}, nil
	}
	return resolveBinaryArithmetic(arithmeticCandidates(operator), left, right)
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
	var active [len(arithmeticAddSignatures)]bool
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
		for i := range signatures {
			if active[i] {
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
