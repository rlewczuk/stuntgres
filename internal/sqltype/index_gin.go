package sqltype

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
)

// LookupGinIndexClass supplies bootstrap facts only. Live binding uses catalog
// descriptors and the selected family's registered support capabilities.
func LookupGinIndexClass(name string) (IndexClass, bool) {
	d := IndexClassDefinition{Name: name, FamilyName: name, Method: 2742, Gin: true}
	switch name {
	case "array_ops":
		d.OID, d.Family, d.Input, d.Storage = 10064, 2745, 2277, 2283
	case "tsvector_ops":
		d.OID, d.Family, d.Input, d.Storage = 10073, 3659, uint32(TSVector), uint32(Text)
	case "jsonb_ops":
		d.OID, d.Family, d.Input, d.Storage = 10090, 4036, uint32(JSONB), uint32(Text)
	case "jsonb_path_ops":
		d.OID, d.Family, d.Input, d.Storage = 10091, 4037, uint32(JSONB), uint32(Int4)
	default:
		return IndexClass{}, false
	}
	return NewIndexClass(d), true
}

// GinToken categories cannot alias ordinary elements, lexemes, or each other.
type GinTokenKind uint8

const (
	GinOrdinary GinTokenKind = iota
	GinNullElement
	GinEmptyItem
	GinNullItem
)

type GinToken struct {
	Kind    GinTokenKind
	Element Value
	Lexeme  string
}

// GinBinding owns the exact key/element generations and scalar comparison.
// Its dictionary ordering is internal, not an SQL ordering/index capability.
type GinBinding struct {
	Usage      Usage
	Class      IndexClass
	element    *Descriptor
	comparison Comparison
	scalar     bool
	jsonb      ginJSONBProvider
}

func BindGinTokens(u Usage, class IndexClass) (GinBinding, error) {
	p := GinBinding{Usage: u, Class: class}
	if !class.IsGin() || !class.Accepts(u.Type) {
		return p, fmt.Errorf("invalid GIN token binding")
	}
	if class.InputOID() == 2277 {
		p.element = u.Type.BaseType().Element()
		base := p.element.BaseType()
		_, ordering := ComparisonCapabilities(base)
		if !ordering {
			return p, failure("0A000", "GIN array element token provider is unsupported")
		}
		var err error
		p.comparison, err = BindComparison(base, base, u.Collation, false)
		return p, err
	}
	switch class.InputOID() {
	case uint32(Bit), uint32(VarBit), 3500, uint32(Money), uint32(Inet), uint32(Cidr), uint32(Macaddr), uint32(Macaddr8), uint32(UUID), uint32(Oid),
		uint32(Int2), uint32(Int4), uint32(Int8), uint32(Float4), uint32(Float8), uint32(Bool),
		uint32(Date), uint32(Time), uint32(Timetz), uint32(Timestamp), uint32(Timestamptz), uint32(Interval),
		uint32(Text), uint32(VarChar), uint32(BPChar), uint32(Char), uint32(Name), uint32(Bytea):
		if class.Support(1).Authority == nil {
			return p, fmt.Errorf("missing scalar GIN installation authority")
		}
		p.scalar = true
		p.element = u.Type
		base := u.Type.BaseType()
		var err error
		p.comparison, err = BindComparison(base, base, u.Collation, false)
		return p, err
	}
	if class.InputOID() == uint32(JSONB) {
		switch class.FamilyOID() {
		case 4036:
			p.jsonb = ginJSONBOps
		case 4037:
			p.jsonb = ginJSONBPathOps
		default:
			return p, failure("0A000", "GIN JSONB token provider is unsupported")
		}
		return p, nil
	}
	if class.InputOID() != uint32(TSVector) {
		return p, failure("0A000", "GIN token provider is unsupported")
	}
	return p, nil
}
func (p GinBinding) Same(other GinBinding) bool {
	return p.Usage == other.Usage && p.Class == other.Class && p.element == other.element && p.scalar == other.scalar && p.jsonb == other.jsonb && p.comparison.Equal(other.comparison)
}

func (p GinBinding) IsScalar() bool { return p.scalar }

// IsJSONB reports the lexeme-provider discriminator. It grants no ordering or
// SQL predicate authority; the scalar jsonb matcher remains the sole authority.
func (p GinBinding) IsJSONB() bool { return p.jsonb != ginJSONBNone }

// JSONBPathOps selects the path-hash dictionary rather than flag+payload keys.
func (p GinBinding) JSONBPathOps() bool { return p.jsonb == ginJSONBPathOps }

// WithElementComparison captures the default B-tree comparator selected for
// array keys, whose GIN class has no explicit comparison support member.
func (p GinBinding) WithElementComparison(comparison Comparison) (GinBinding, error) {
	if p.scalar || p.element == nil || comparison.left != p.comparison.left || comparison.right != p.comparison.right {
		return GinBinding{}, fmt.Errorf("invalid GIN element comparison capture")
	}
	p.comparison = comparison
	return p, nil
}

func (p GinBinding) ElementComparison() (Comparison, bool) {
	return p.comparison, p.element != nil && !p.scalar
}
func (p GinBinding) Extract(ctx context.Context, v Value, emit func(GinToken) error) error {
	return p.ExtractWithEnvironment(ctx, v, emit, InputEnvironment{})
}
func (p GinBinding) ExtractWithEnvironment(ctx context.Context, v Value, emit func(GinToken) error, input InputEnvironment) error {
	return p.extractWithEnvironment(ctx, v, emit, input, 2)
}

// ExtractQueryWithEnvironment is the selected extractQuery callback, not a
// replay of extractValue for a probe. The native token representation is shared.
func (p GinBinding) ExtractQueryWithEnvironment(ctx context.Context, v Value, emit func(GinToken) error, input InputEnvironment) error {
	return p.extractWithEnvironment(ctx, v, emit, input, 3)
}

func (p GinBinding) extractWithEnvironment(ctx context.Context, v Value, emit func(GinToken) error, input InputEnvironment, slot uint16) error {
	extracted, err := p.PrepareExtract(ctx, v, input, slot)
	if err != nil {
		return err
	}
	return extracted.Emit(ctx, emit)
}
func (p GinBinding) Compare(ctx context.Context, a, b GinToken, input InputEnvironment) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if a.Kind > GinNullItem || b.Kind > GinNullItem {
		return 0, fmt.Errorf("invalid GIN token category")
	}
	if p.element == nil && (a.Element.typ != nil || b.Element.typ != nil) {
		return 0, fmt.Errorf("non-lexeme GIN token")
	}
	if p.element != nil && (a.Lexeme != "" || b.Lexeme != "") {
		return 0, fmt.Errorf("non-element GIN token")
	}
	if a.Kind < b.Kind {
		return -1, nil
	}
	if a.Kind > b.Kind {
		return 1, nil
	}
	if a.Kind != GinOrdinary {
		return 0, nil
	}
	if p.scalar || p.element == nil {
		body, err := enterIndexSupport(input, p.Class.Support(1), 1)
		if err != nil {
			return 0, err
		}
		defer body.Leave()
	}
	if p.element == nil {
		switch p.jsonb {
		case ginJSONBOps:
			if a.Lexeme == "" || b.Lexeme == "" {
				return 0, fmt.Errorf("empty JSONB GIN token")
			}
			return strings.Compare(a.Lexeme, b.Lexeme), nil
		case ginJSONBPathOps:
			if len(a.Lexeme) != 4 || len(b.Lexeme) != 4 {
				return 0, fmt.Errorf("invalid JSONB path GIN token")
			}
			left := int32(binary.BigEndian.Uint32([]byte(a.Lexeme)))
			right := int32(binary.BigEndian.Uint32([]byte(b.Lexeme)))
			switch {
			case left < right:
				return -1, nil
			case left > right:
				return 1, nil
			}
			return 0, nil
		}
		return strings.Compare(a.Lexeme, b.Lexeme), nil
	}
	if a.Element.typ != p.element || b.Element.typ != p.element {
		return 0, fmt.Errorf("foreign GIN token generation")
	}
	base := p.element.BaseType()
	if p.scalar && p.Class.InputOID() == uint32(BPChar) {
		if a.Element.null || b.Element.null {
			return 0, fmt.Errorf("invalid scalar GIN token")
		}
		return compareIndexBPChar(ctx, p.comparison.inputCollation, a.Element, b.Element)
	}
	if p.scalar && p.Class.InputOID() == uint32(Oid) && base.id == Int4 {
		if a.Element.null || b.Element.null {
			return 0, fmt.Errorf("invalid scalar GIN token")
		}
		return compareIndexOIDInt4(a.Element, b.Element), nil
	}
	a.Element.typ, b.Element.typ = base, base
	return p.comparison.Compare(ctx, a.Element, b.Element, input)
}
