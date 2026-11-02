package sqltype

import (
	"context"
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
	case uint32(Bit), uint32(VarBit), 3500, uint32(Money):
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
	if class.InputOID() != uint32(TSVector) {
		return p, failure("0A000", "GIN token provider is unsupported")
	}
	return p, nil
}
func (p GinBinding) Same(other GinBinding) bool {
	return p.Usage == other.Usage && p.Class == other.Class && p.element == other.element && p.scalar == other.scalar && p.comparison.Equal(other.comparison)
}

func (p GinBinding) IsScalar() bool { return p.scalar }
func (p GinBinding) Extract(ctx context.Context, v Value, emit func(GinToken) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if v.typ != p.Usage.Type {
		return fmt.Errorf("foreign GIN key generation")
	}
	if v.null {
		return emit(GinToken{Kind: GinNullItem})
	}
	if p.scalar {
		return emit(GinToken{Element: v})
	}
	if p.element != nil {
		if v.array == nil {
			return fmt.Errorf("invalid GIN array payload")
		}
		if v.ArrayLen() == 0 {
			return emit(GinToken{Kind: GinEmptyItem})
		}
		for i := range v.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return err
			}
			element := v.ArrayElement(i)
			if element.typ != p.element {
				return fmt.Errorf("foreign GIN element generation")
			}
			token := GinToken{Element: element}
			if element.IsNull() {
				token = GinToken{Kind: GinNullElement}
			}
			if err := emit(token); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	lexemes, err := TSVectorLexemes(v, p.Usage.Type)
	if err != nil {
		return err
	}
	if lexemes.Count() == 0 {
		return emit(GinToken{Kind: GinEmptyItem})
	}
	for i := range lexemes.Count() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := emit(GinToken{Lexeme: lexemes.At(i)}); err != nil {
			return err
		}
	}
	return ctx.Err()
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
	if p.element == nil {
		return strings.Compare(a.Lexeme, b.Lexeme), nil
	}
	if a.Element.typ != p.element || b.Element.typ != p.element {
		return 0, fmt.Errorf("foreign GIN token generation")
	}
	a.Element.typ, b.Element.typ = p.element.BaseType(), p.element.BaseType()
	return p.comparison.Compare(ctx, a.Element, b.Element, input)
}
