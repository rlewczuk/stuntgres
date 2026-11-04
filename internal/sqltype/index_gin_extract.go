package sqltype

import (
	"context"
	"fmt"
)

// GinExtraction is the immutable native extract result. It borrows existing
// datum storage, not the entered callback or its execution environment.
type GinExtraction struct {
	value   Value
	element *Descriptor
	lexemes TSVectorLexemeView
	scalar  bool
}

// PrepareExtract runs exactly the selected whole-input value/query callback.
// Its lexical body ends before the caller consumes tokens or compares them.
func (p GinBinding) PrepareExtract(ctx context.Context, v Value, input InputEnvironment, slot uint16) (GinExtraction, error) {
	if err := ctx.Err(); err != nil {
		return GinExtraction{}, err
	}
	if v.typ != p.Usage.Type || slot != 2 && slot != 3 {
		return GinExtraction{}, fmt.Errorf("foreign GIN extraction authority")
	}
	if v.null {
		return GinExtraction{value: v, element: p.element, scalar: p.scalar}, nil
	}
	body, err := enterIndexSupport(input, p.Class.Support(int(slot)), slot)
	if err != nil {
		return GinExtraction{}, err
	}
	defer body.Leave()
	result := GinExtraction{value: v, element: p.element, scalar: p.scalar}
	if p.scalar {
		return result, nil
	}
	if p.element != nil {
		if v.array == nil {
			return GinExtraction{}, fmt.Errorf("invalid GIN array payload")
		}
		for i := range v.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return GinExtraction{}, err
			}
			if v.ArrayElement(i).typ != p.element {
				return GinExtraction{}, fmt.Errorf("foreign GIN element generation")
			}
		}
		return result, nil
	}
	result.lexemes, err = TSVectorLexemes(v, p.Usage.Type)
	return result, err
}

func (p GinExtraction) Emit(ctx context.Context, emit func(GinToken) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.value.null {
		return emit(GinToken{Kind: GinNullItem})
	}
	if p.scalar {
		return emit(GinToken{Element: p.value})
	}
	if p.element != nil {
		if p.value.ArrayLen() == 0 {
			return emit(GinToken{Kind: GinEmptyItem})
		}
		for i := range p.value.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return err
			}
			element := p.value.ArrayElement(i)
			token := GinToken{Element: element}
			if element.IsNull() {
				token = GinToken{Kind: GinNullElement}
			}
			if err := emit(token); err != nil {
				return err
			}
		}
	} else {
		if p.lexemes.Count() == 0 {
			return emit(GinToken{Kind: GinEmptyItem})
		}
		for i := range p.lexemes.Count() {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := emit(GinToken{Lexeme: p.lexemes.At(i)}); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
