package catalog

// ForeignColumnInputError identifies a local input failure by its admitted
// projected stable attribute. The executor, not the provider, owns relation and
// column alias diagnostics. This carrier adds no SQLSTATE or remote context.
type ForeignColumnInputError struct {
	Attribute int32
	Err       error
}

func (e *ForeignColumnInputError) Error() string {
	if e.Err == nil {
		return "stuntgres: missing foreign column input cause"
	}
	return e.Err.Error()
}
func (e *ForeignColumnInputError) Unwrap() error { return e.Err }
