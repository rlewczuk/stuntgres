package sqltype

// EqualitySignature contains the instantiated operand targets of a selected
// comparison. Generic targets retain their concrete descriptor identities.
type EqualitySignature struct{ Left, Right ID }

var equalitySignatures = [...]EqualitySignature{
	{Oid, Oid},
	{Tid, Tid},
	{Macaddr, Macaddr},
	{Box, Box},
	{Line, Line},
	{Lseg, Lseg},
	{Path, Path},
	{Point, Point},
	{Xid, Xid}, {Xid, Int4}, {Cid, Cid}, {Xid8, Xid8},
	{Int2, Int2}, {Int2, Int4}, {Int2, Int8}, {Int4, Int2}, {Int4, Int4}, {Int4, Int8}, {Int8, Int2}, {Int8, Int4}, {Int8, Int8},
	{Float4, Float4}, {Float4, Float8}, {Float8, Float4}, {Float8, Float8}, {Numeric, Numeric}, {Bool, Bool},
	{Money, Money},
	{Inet, Inet},
	{Circle, Circle},
	{Char, Char},
	{Text, Text}, {BPChar, BPChar}, {Name, Name}, {Name, Text}, {Text, Name},
	{Date, Date}, {Time, Time}, {Timestamp, Timestamp}, {Date, Timestamp}, {Timestamp, Date}, {UUID, UUID}, {Bytea, Bytea},
	{Timestamptz, Timestamptz}, {Timetz, Timetz},
	{Date, Timestamptz}, {Timestamptz, Date}, {Timestamp, Timestamptz}, {Timestamptz, Timestamp},
	{Interval, Interval}, {Bit, Bit}, {VarBit, VarBit}, {JSONB, JSONB}, {ACLItem, ACLItem},
}

func equalityCategory(id ID) byte {
	switch id {
	case Regclass, Regtype, Regrole, Regproc, Regprocedure, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		return 'N'
	case Char, PgNodeTree:
		return 'Z'
	case Bit, VarBit:
		return 'V'
	case Inet, Cidr:
		return 'I'
	case Circle, Path:
		return 'G'
	case Bool:
		return 'B'
	case Oid, Int2, Int4, Int8, Numeric, Money, Float4, Float8:
		return 'N'
	case Text, VarChar, BPChar, Name:
		return 'S'
	case Date, Time, Timestamp, Timestamptz, Timetz:
		return 'D'
	case Interval:
		return 'T'
	case UUID, Bytea, Tid, Macaddr, Xid, Cid, Xid8:
		return 'U'
	}
	return 0
}
func equalityPreferred(id ID) bool {
	return id == Inet || id == Oid || id == Text || id == Float8 || id == Bool || id == Timestamptz || id == Interval
}
func equalityImplicit(from, to ID) bool {
	if from == to {
		return true
	}
	if (IsRoutineAlias(from) || IsOperatorAlias(from) || IsRegObjectAlias(from) || IsRegTS(from)) && to == Oid {
		return true
	}
	if from == Cidr && to == Inet || from == Bit && to == VarBit {
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
	return ResolveComparison(CompareEqual, left, right)
}

// ResolveComparison follows released binary operator resolution: exact/unknown
// shortcuts, then direct implicit candidates and the prescribed best-match
// filters. Provider demand is separate from generic operator selection.
func ResolveComparison(operator ComparisonOperator, left, right *Descriptor) (EqualitySignature, error) {
	if operator > CompareGreaterEqual {
		return EqualitySignature{}, failure("42883", "incompatible comparison operands")
	}
	var l, r ID
	if left != nil {
		l = left.BaseType().ID()
	}
	if right != nil {
		r = right.BaseType().ID()
	}
	known := left
	if known == nil {
		known = right
	}
	if known != nil {
		base := known.BaseType()
		other := right
		if left == nil {
			other = nil
		}
		// The canonical catalog NULL carrier matches anyarray's signature,
		// but two unresolved family members cannot determine an element type.
		// This is analysis failure, not input or comparison-provider admission.
		if base.Usage().CatalogNullUsage() && (other == nil || other.BaseType().Usage().CatalogNullUsage()) {
			return EqualitySignature{}, failure("42804", `cannot determine element type of "anyarray" argument`)
		}
		if base.IsComposite() || base.IsDeferredRecord() {
			if other == nil || other.BaseType().IsComposite() || other.BaseType().IsDeferredRecord() {
				if other == nil {
					if left == nil {
						return EqualitySignature{2249, base.ID()}, nil
					}
					return EqualitySignature{base.ID(), 2249}, nil
				}
				return EqualitySignature{base.ID(), other.BaseType().ID()}, nil
			}
			return EqualitySignature{}, failure("42883", "incompatible comparison operands")
		}
		if base.IsArray() || base.IsEnum() || isRange(base.ID()) || isMultirange(base.ID()) {
			// anyenum accepts actual enums, not domains over enums.
			if base.IsEnum() && (known.IsDomain() || other != nil && other.IsDomain()) ||
				other != nil && other.BaseType() != base {
				return EqualitySignature{}, failure("42883", "incompatible comparison operands")
			}
			return EqualitySignature{base.ID(), base.ID()}, nil
		}
	}
	for _, s := range equalitySignatures {
		if !comparisonSignatureAvailable(operator, s) {
			continue
		}
		if left != nil && right != nil && !left.IsDomain() && !right.IsDomain() && s.Left == l && s.Right == r ||
			left == nil && right != nil && s.Left == r && s.Right == r ||
			right == nil && left != nil && s.Left == l && s.Right == l {
			return s, nil
		}
	}
	var active [len(equalitySignatures)]bool
	count := 0
	for i, s := range equalitySignatures {
		if comparisonSignatureAvailable(operator, s) && (left == nil || equalityImplicit(l, s.Left)) && (right == nil || equalityImplicit(r, s.Right)) {
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

func comparisonSignatureAvailable(operator ComparisonOperator, s EqualitySignature) bool {
	switch s.Left {
	case Point:
		return operator == CompareNotEqual
	case Box, Path:
		return operator != CompareNotEqual
	case Line, ACLItem, Cid:
		return operator == CompareEqual
	case Xid:
		return operator == CompareEqual || operator == CompareNotEqual
	}
	return true
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
