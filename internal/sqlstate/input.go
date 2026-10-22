package sqlstate

// InputError records the producer's error-save boundary, not a SQLSTATE policy.
// Its cause remains the public diagnostic; recovery inspects only this direct
// marker, never an arbitrary wrapped cause. The representation is shared by
// parser input, catalog-alias input and hard domain CHECK evaluation.
type InputError struct {
	cause inputCause
	soft  bool
}

type inputCause interface {
	error
	SQLState() string
}

func (e *InputError) Error() string     { return e.cause.Error() }
func (e *InputError) SQLState() string  { return e.cause.SQLState() }
func (e *InputError) Unwrap() error     { return e.cause }
func (e *InputError) Recoverable() bool { return e.soft }

// SoftInputError marks only an input-owned failure whose released producer uses
// an error-save return. A previously marked hard cause must never be softened.
func SoftInputError(err error) error {
	if _, marked := err.(*InputError); marked {
		return err
	}
	if cause, ok := err.(inputCause); ok {
		return &InputError{cause: cause, soft: true}
	}
	return err
}

// HardInputError preserves helper/CHECK exceptions even when their SQLSTATE is
// also used by soft primitive input. Ordinary Go/context errors remain untouched
// and never acquire a synthetic SQLState interface.
func HardInputError(err error) error {
	if marked, ok := err.(*InputError); ok && !marked.soft {
		return err
	}
	if cause, ok := err.(inputCause); ok {
		return &InputError{cause: cause}
	}
	return err
}

// InputDiagnostic preserves direct producer provenance when a presentation
// owner copies a diagnostic to attach a position or context.
func InputDiagnostic(original error, diagnostic *Error) error {
	if marked, ok := original.(*InputError); ok {
		return &InputError{cause: diagnostic, soft: marked.soft}
	}
	return diagnostic
}
