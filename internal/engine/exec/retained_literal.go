package exec

import "stuntgres/internal/engine/expr"

// FoldOriginOf reads an opaque stored occurrence annotation, if present.
func FoldOriginOf(node expr.Expr) FoldOrigin {
	if annotated, ok := node.(expr.Annotated); ok {
		origin, _ := annotated.Annotation().(FoldOrigin)
		return origin
	}
	return FoldOrigin{}
}

