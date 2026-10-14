package functions

// CountSignature describes only the intrinsic argument shape. Catalog visibility
// and diagnostics remain with the engine binder.
type CountSignature uint8

const (
	CountCallable CountSignature = iota
	CountTooMany
	CountMismatch
	CountRequiresStar
)

func AnalyzeCountSignature(countName, star bool, arity int) CountSignature {
	if arity > 100 {
		return CountTooMany
	}
	if !countName || (!star && arity > 1) {
		return CountMismatch
	}
	if !star && arity == 0 {
		return CountRequiresStar
	}
	return CountCallable
}
