package sqltype

import "fmt"

// EncodingConversionReference is an immutable selected callback association.
// It carries no request, session, body token or permission result.
type EncodingConversionReference interface {
	EncodingConversionHandler() EncodingConversionHandler
}
type EncodingConversionBoundary interface {
	EnterEncodingConversion(EncodingConversionReference) (RoutineBodyToken, error)
}

func (h EncodingConversionHandler) WithReference(reference EncodingConversionReference) EncodingConversionHandler {
	h.reference = reference
	return h
}
func (h EncodingConversionHandler) enter(boundary EncodingConversionBoundary) (RoutineBodyToken, error) {
	if h.reference == nil {
		return RoutineBodyToken{}, nil
	}
	if boundary == nil {
		return RoutineBodyToken{}, fmt.Errorf("stuntgres: missing actual encoding conversion owner")
	}
	return boundary.EnterEncodingConversion(h.reference)
}
