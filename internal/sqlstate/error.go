package sqlstate

// Error is a PostgreSQL-compatible diagnostic with a five-character SQLSTATE.
// It also carries nonfatal notices and warnings independently of returned errors.
// Empty optional fields are absent. Positions are one-based character indexes.
// InternalPosition requires InternalQuery; InternalQuery alone is permitted.
// File, Line and Routine describe implementation-specific source locations.
type Error struct {
	Severity            string
	SeverityUnlocalized string
	Code                string
	Message             string
	Detail              string
	Hint                string
	Position            string
	InternalPosition    string
	InternalQuery       string
	Context             string
	Schema              string
	Table               string
	Column              string
	DataType            string
	Constraint          string
	File                string
	Line                string
	Routine             string
}

func (e *Error) Error() string { return "stuntgres: " + e.Message }

func (e *Error) SQLState() string { return e.Code }

// WithDiagnostic replaces the visible diagnostic without discarding the
// originating error's identity, classifications or secondary failures.
// errors.As sees the decorated diagnostic first; the cause stays traversable.
func WithDiagnostic(cause error, diagnostic *Error) error {
	return &diagnosticError{diagnostic: diagnostic, cause: cause}
}

type diagnosticError struct {
	diagnostic *Error
	cause      error
}

func (e *diagnosticError) Error() string    { return e.diagnostic.Error() }
func (e *diagnosticError) SQLState() string { return e.diagnostic.Code }
func (e *diagnosticError) Unwrap() error    { return e.cause }
func (e *diagnosticError) As(target any) bool {
	if diagnostic, ok := target.(**Error); ok {
		*diagnostic = e.diagnostic
		return true
	}
	return false
}
