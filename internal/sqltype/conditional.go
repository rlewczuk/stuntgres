package sqltype

// EqualitySignature is a supported builtin '=' operand signature, not a
// catalog identity. Unknown operands are represented by nil during analysis.
type EqualitySignature struct{ Left, Right ID }

var equalitySignatures = [...]EqualitySignature{
	{Oid, Oid},
	{Int2, Int2}, {Int2, Int4}, {Int2, Int8}, {Int4, Int2}, {Int4, Int4}, {Int4, Int8}, {Int8, Int2}, {Int8, Int4}, {Int8, Int8},
	{Float4, Float4}, {Float4, Float8}, {Float8, Float4}, {Float8, Float8}, {Numeric, Numeric}, {Bool, Bool},
	{Text, Text}, {BPChar, BPChar}, {Name, Name}, {Name, Text}, {Text, Name},
	{Date, Date}, {Time, Time}, {Timestamp, Timestamp}, {Date, Timestamp}, {Timestamp, Date}, {UUID, UUID}, {Bytea, Bytea},
}

func equalityCategory(id ID) byte {
	switch id {
	case Bool:
		return 'B'
	case Oid, Int2, Int4, Int8, Numeric, Float4, Float8:
		return 'N'
	case Text, VarChar, BPChar, Name:
		return 'S'
	case Date, Time, Timestamp:
		return 'D'
	case UUID, Bytea:
		return 'U'
	}
	return 0
}
func equalityPreferred(id ID) bool { return id == Oid || id == Text || id == Float8 || id == Bool }
func equalityImplicit(from, to ID) bool {
	if from == to {
		return true
	}
	if IsCharacter(from) && IsCharacter(to) {
		return true
	}
	for _, cast := range baselineCasts {
		if cast.source == from && cast.target == to && cast.minimum == Implicit {
			return true
		}
	}
	return false
}
func ResolveEquality(left, right *Descriptor) (EqualitySignature, error) {
	var l, r ID
	if left != nil {
		l = left.ID()
	}
	if right != nil {
		r = right.ID()
	}
	for _, s := range equalitySignatures {
		if left != nil && right != nil && s.Left == l && s.Right == r || left == nil && right != nil && s.Left == r && s.Right == r || right == nil && left != nil && s.Left == l && s.Right == l {
			return s, nil
		}
	}
	var active [len(equalitySignatures)]bool
	count := 0
	for i, s := range equalitySignatures {
		if (left == nil || equalityImplicit(l, s.Left)) && (right == nil || equalityImplicit(r, s.Right)) {
			active[i] = true
			count++
		}
	}
	choose := func() (EqualitySignature, bool) {
		if count != 1 {
			return EqualitySignature{}, false
		}
		for i, ok := range active {
			if ok {
				return equalitySignatures[i], true
			}
		}
		return EqualitySignature{}, false
	}
	if s, ok := choose(); ok {
		return s, nil
	}
	for phase := range 2 {
		best := -1
		for i, s := range equalitySignatures {
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
		for i, s := range equalitySignatures {
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
		for i, s := range equalitySignatures {
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
		for i, s := range equalitySignatures {
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
		for i, s := range equalitySignatures {
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
		var selected EqualitySignature
		for i, s := range equalitySignatures {
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
		return EqualitySignature{}, failure("42883", "incompatible equality operands")
	}
	return EqualitySignature{}, failure("42725", "ambiguous equality operands")
}

func compareDateTimestamp(day, stamp int64) int {
	if day == DateNegativeInfinity || stamp == TimestampPositiveInfinity {
		if day == stamp {
			return 0
		}
		return -1
	}
	if day == DatePositiveInfinity || stamp == TimestampNegativeInfinity {
		if day == stamp {
			return 0
		}
		return 1
	}
	stampDay := floorDiv(stamp, microsPerDay)
	if day < stampDay {
		return -1
	}
	if day > stampDay {
		return 1
	}
	if stamp-stampDay*microsPerDay != 0 {
		return -1
	}
	return 0
}

// BindEqualityComparison supplies the conditional-only cross-temporal builtin
// without widening the legacy general predicate admission.
func BindEqualityComparison(left, right *Descriptor) (Comparison, error) {
	if left != nil && right != nil && left.Concrete() && right.Concrete() && (left.ID() == Name && right.ID() == Text || left.ID() == Text && right.ID() == Name) {
		return Comparison{left, right}, nil
	}
	if left != nil && right != nil && left.Concrete() && right.Concrete() && (left.ID() == Date && right.ID() == Timestamp || left.ID() == Timestamp && right.ID() == Date) {
		return Comparison{left, right}, nil
	}
	return BindComparison(left, right)
}
