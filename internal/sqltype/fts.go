package sqltype

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/fts"
)

func IsFTS(id ID) bool { return id == TSVector || id == TSQuery }

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
