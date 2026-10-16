package sqltype

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"stuntgres/internal/sqlstate"
)

// Grouping binds NOT DISTINCT equality and a compatible hash to one concrete
// key usage. SQL strict comparison and its NULL semantics are unchanged.
type Grouping struct {
	usage      Usage
	comparison Comparison
	element    *Grouping
	prepare    bool
}

func BindGrouping(usage Usage) (Grouping, error) {
	if usage.Type == nil || !usage.Type.Concrete() {
		return Grouping{}, fmt.Errorf("invalid grouping usage")
	}
	if usage.Type.BaseType().id == JSON {
		return Grouping{}, failure("42883", "could not identify an equality operator for type json")
	}
	if !usage.validCollation() {
		return Grouping{}, fmt.Errorf("invalid grouping collation")
	}
	g := Grouping{usage: usage}
	base := usage.Type.BaseType()
	if base.IsArray() {
		elementUsage := base.element.Usage()
		elementUsage.Collation = usage.Collation
		element, err := BindGrouping(elementUsage)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42883" {
				copy := *diagnostic
				copy.Message = "could not identify an equality operator for type " + usage.Type.DiagnosticName()
				return Grouping{}, &copy
			}
			return Grouping{}, err
		}
		g.element, g.prepare = &element, element.prepare
		return g, nil
	}
	if base.IsEnum() {
		return g, nil
	}
	if base.id == PgNodeTree {
		g.prepare = true
		return g, nil
	}
	if IsTransactionIdentifier(base.id) {
		return g, nil
	}
	switch base.id {
	case Bool, Bytea, Char, Name, Int2, Int4, Int8, Tid, Money, Text, Oid, BPChar, VarChar, Float4, Float8, Date, Time, Timestamp, Timestamptz, Timetz, Interval, Numeric, UUID, Regclass, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary, JSONB:
	default:
		return Grouping{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "could not identify an equality operator for type " + usage.Type.DiagnosticName()}
	}
	comparison, err := BindComparison(usage.Type, usage.Type, usage.Collation, false)
	if err != nil {
		return Grouping{}, err
	}
	g.comparison = comparison
	return g, nil
}

// EqualityGrouping binds hash membership to the predicate's retained authority.
func (p ComparisonPredicate) EqualityGrouping() (Grouping, error) {
	if p.operator != CompareEqual || p.operands.left != p.operands.right {
		return Grouping{}, fmt.Errorf("comparison is not a same-type hash equality")
	}
	usage := p.operands.left.Usage()
	usage.Collation = p.operands.inputCollation
	g, err := BindGrouping(usage)
	if err != nil {
		return Grouping{}, err
	}
	g.comparison = p.operands
	return g, nil
}
func (g Grouping) check(v Value) error {
	if g.usage.Type == nil || v.typ != g.usage.Type {
		return fmt.Errorf("invalid grouping value")
	}
	if !v.null && v.typ.BaseType().IsEnum() {
		base := v.typ.BaseType()
		value := v
		value.typ = base
		if _, err := base.enumOutput(value); err != nil {
			return err
		}
	}
	if !v.null && v.typ.BaseType().id == Regclass {
		if len(v.text) == 0 || len(v.text) > 1 && v.text[0] == '0' {
			return fmt.Errorf("invalid canonical regclass grouping value")
		}
		for i := range len(v.text) {
			if v.text[i] < '0' || v.text[i] > '9' {
				return fmt.Errorf("invalid canonical regclass grouping value")
			}
		}
		if _, err := strconv.ParseUint(v.text, 10, 32); err != nil {
			return fmt.Errorf("invalid canonical regclass grouping value")
		}
	}
	return nil
}
func (g Grouping) Equal(ctx context.Context, a, b Value) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := g.check(a); err != nil {
		return false, err
	}
	if err := g.check(b); err != nil {
		return false, err
	}
	if a.null || b.null {
		return a.null == b.null, ctx.Err()
	}
	if g.comparison.collationIndeterminate {
		return false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}
	}
	if g.element != nil {
		if a.array == nil || b.array == nil {
			return false, fmt.Errorf("missing grouping array payload")
		}
		if len(a.array.dimensions) != len(b.array.dimensions) {
			return false, nil
		}
		for i, dim := range a.array.dimensions {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if dim != b.array.dimensions[i] {
				return false, nil
			}
		}
		for i, value := range a.array.elements {
			equal, err := g.element.Equal(ctx, value, b.array.elements[i])
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, ctx.Err()
	}
	if a.typ.BaseType().IsEnum() {
		return a.integer == b.integer, ctx.Err()
	}
	if a.typ.BaseType().id == PgNodeTree {
		var err error
		a, err = g.PrepareKey(ctx, a)
		if err != nil {
			return false, err
		}
		b, err = g.PrepareKey(ctx, b)
		if err != nil {
			return false, err
		}
		return groupingTextEqual(ctx, a.text, b.text)
	}
	switch a.typ.BaseType().id {
	case Xid, Cid, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		return a.oid == b.oid, ctx.Err()
	case Xid8:
		return a.integer == b.integer, ctx.Err()
	case Text, VarChar, Name:
		return g.usage.Collation.EqualText(ctx, a.text, b.text)
	case Bytea, Regclass, Char:
		return groupingTextEqual(ctx, a.text, b.text)
	case BPChar:
		return g.usage.Collation.EqualText(ctx, strings.TrimRight(a.text, " "), strings.TrimRight(b.text, " "))
	}
	n, err := g.comparison.Compare(ctx, a, b, InputEnvironment{})
	if err != nil {
		return false, err
	}
	return n == 0, ctx.Err()
}
func (g Grouping) Hash(ctx context.Context, v Value) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := g.check(v); err != nil {
		return 0, err
	}
	v.typ = v.typ.BaseType()
	h := uint64(14695981039346656037)
	byteHash := func(b byte) { h = (h ^ uint64(b)) * 1099511628211 }
	integerHash := func(n uint64) {
		for range 8 {
			byteHash(byte(n))
			n >>= 8
		}
	}
	textHash := func(s string) error {
		var err error
		h, err = groupingTextHash(ctx, h, s)
		return err
	}
	integerHash(uint64(v.typ.id))
	if v.null {
		byteHash(0)
		return h, ctx.Err()
	}
	if g.comparison.collationIndeterminate {
		return 0, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P22", Message: "could not determine which collation to use for string hashing", Hint: "Use the COLLATE clause to set the collation explicitly."}
	}
	byteHash(1)
	if v.typ.IsEnum() {
		integerHash(uint64(v.integer))
		return h, ctx.Err()
	}
	if g.element != nil {
		if v.array == nil {
			return 0, fmt.Errorf("missing grouping array payload")
		}
		integerHash(uint64(len(v.array.dimensions)))
		for _, dim := range v.array.dimensions {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			integerHash(uint64(int64(dim.Lower)))
			integerHash(uint64(dim.Length))
		}
		for _, value := range v.array.elements {
			key, err := g.element.Hash(ctx, value)
			if err != nil {
				return 0, err
			}
			integerHash(key)
		}
		return h, ctx.Err()
	}
	switch v.typ.id {
	case JSONB:
		if v.jsonb == nil {
			return 0, fmt.Errorf("missing JSONB payload")
		}
		return hashJSONB(ctx, &v.jsonb.root, h)
	case PgNodeTree:
		// Preserve the logical descriptor for PrepareKey's generation check.
		v.typ = g.usage.Type
		prepared, err := g.PrepareKey(ctx, v)
		if err != nil {
			return 0, err
		}
		if err := textHash(prepared.text); err != nil {
			return 0, err
		}
	case Interval:
		byteHash(byte(intervalInfinity(v) + 1))
		if intervalInfinity(v) == 0 {
			hi, lo := intervalSpan(v)
			integerHash(uint64(hi))
			integerHash(lo)
		}
	case Oid, Xid, Cid, Regtype, Regproc, Regprocedure, Regrole, Regoper, Regoperator, Regcollation, Regnamespace, Regconfig, Regdictionary:
		integerHash(uint64(v.oid))
	case Bool:
		if v.boolean {
			byteHash(1)
		} else {
			byteHash(0)
		}
	case Int2, Int4, Int8, Tid, Xid8, Money, Date, Time, Timestamp, Timestamptz:
		integerHash(uint64(v.integer))
	case Timetz:
		integerHash(uint64(v.integer))
		integerHash(uint64(int64(v.west)))
	case Float4, Float8:
		bits := math.Float64bits(v.floating)
		if math.IsNaN(v.floating) {
			bits = 0x7ff8000000000000
		} else if v.floating == 0 {
			bits = 0
		}
		integerHash(bits)
	case Numeric:
		byteHash(v.special)
		if v.special == numericFinite {
			v = normalizeNumeric(v)
			if v.negative {
				byteHash(1)
			} else {
				byteHash(0)
			}
			integerHash(uint64(v.exponent))
			if err := textHash(v.text); err != nil {
				return 0, err
			}
		}
	case Text, VarChar, BPChar, Name, Bytea, Regclass, Char:
		s := v.text
		if v.typ.id == BPChar {
			s = strings.TrimRight(s, " ")
		}
		if g.usage.Collation.ID != 0 {
			var err error
			h, err = g.usage.Collation.HashText(ctx, h, s)
			if err != nil {
				return 0, err
			}
		} else if err := textHash(s); err != nil {
			return 0, err
		}
	case UUID:
		for _, b := range v.uuid {
			byteHash(b)
		}
	default:
		return 0, fmt.Errorf("unsupported grouping type")
	}
	return h, ctx.Err()
}

func groupingTextEqual(ctx context.Context, a, b string) (bool, error) {
	if len(a) != len(b) {
		return false, ctx.Err()
	}
	for offset := 0; offset < len(a); offset += 1024 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		end := min(offset+1024, len(a))
		if a[offset:end] != b[offset:end] {
			return false, ctx.Err()
		}
	}
	return true, ctx.Err()
}

func groupingTextHash(ctx context.Context, seed uint64, text string) (uint64, error) {
	for i := range len(text) {
		if i&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		seed = (seed ^ uint64(text[i])) * 1099511628211
	}
	return seed, ctx.Err()
}
