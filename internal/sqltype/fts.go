package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/fts"
)

func IsFTS(id ID) bool { return id == TSVector || id == TSQuery }

// TSVectorValue wraps an immutable decoded vector with the result's SQL type.
func TSVectorValue(d *Descriptor, vector *fts.Vector) (Value, error) {
	if d == nil || d.ID() != TSVector || !vector.Valid() {
		return Value{}, fmt.Errorf("invalid tsvector result authority")
	}
	return Value{typ: d, ftsVector: vector}, nil
}

// TSQueryValue wraps a constructed immutable arena without text re-input.
func TSQueryValue(d *Descriptor, query *fts.Query) (Value, error) {
	if d == nil || d.ID() != TSQuery || !query.Valid() {
		return Value{}, fmt.Errorf("invalid tsquery result authority")
	}
	return Value{typ: d, ftsQuery: query}, nil
}

// LexicalArray projects internal dictionary metadata at the SQL boundary only.
func LexicalArray(d *Descriptor, result fts.LexicalResult) (Value, error) {
	if d == nil || d.ID() != 1009 || !d.IsArray() || d.Element().ID() != Text {
		return Value{}, fmt.Errorf("invalid dictionary result descriptor")
	}
	if !result.Recognized {
		return Null(d), nil
	}
	if len(result.Lexemes) == 0 {
		return OwnedArrayValue(d, nil, nil)
	}
	values := make([]Value, len(result.Lexemes))
	for i, lexeme := range result.Lexemes {
		values[i] = String(d.Element(), lexeme.Text)
	}
	return OwnedArrayValue(d, []ArrayDimension{{Lower: 1, Length: int32(len(values))}}, values)
}

// FTSInputNotices belongs to an operation, never to a stored datum or codec.
type FTSInputNotices interface {
	EmptyQuery(context.Context, string)
	FTSLiteral(int) FTSInputNotices
	FTSParameter(int, bool) FTSInputNotices
}

func (d *Descriptor) ftsInput(ctx context.Context, text string, input InputEnvironment) (Value, error) {
	v := Value{typ: d}
	if d.id == TSVector {
		payload, err := fts.ParseVector(ctx, text)
		if err != nil {
			return Value{}, err
		}
		v.ftsVector = payload
	} else {
		payload, err := fts.ParseQuery(ctx, text)
		if err != nil {
			return Value{}, err
		}
		v.ftsQuery = payload
		if payload.Empty() && input.FTS != nil {
			input.FTS.EmptyQuery(ctx, text)
		}
	}
	return v, nil
}
func (v Value) validFTS() bool {
	return v.typ != nil && ((v.typ.BaseType().id == TSVector && v.ftsVector.Valid() && v.ftsQuery == nil) || (v.typ.BaseType().id == TSQuery && v.ftsQuery.Valid() && v.ftsVector == nil))
}
func (d *Descriptor) ftsOutput(v Value) (string, error) {
	if v.typ != d || v.null || !v.validFTS() {
		return "", fmt.Errorf("invalid FTS payload")
	}
	if d.id == TSVector {
		return v.ftsVector.String(), nil
	}
	return v.ftsQuery.String(), nil
}
func (a Value) sameFTS(ctx context.Context, b Value) (bool, error) {
	if a.ftsVector != nil || b.ftsVector != nil {
		return a.ftsVector.Equal(ctx, b.ftsVector)
	}
	return a.ftsQuery.Equal(ctx, b.ftsQuery)
}
