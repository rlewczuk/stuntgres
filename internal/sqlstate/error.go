package sqlstate

// Error is a PostgreSQL-compatible error with a five-character SQLSTATE.
type Error struct {
	Code    string
	Message string
	Schema  string
	Table   string
	Column  string
}

func (e *Error) Error() string { return "stuntgres: " + e.Message }

func (e *Error) SQLState() string { return e.Code }
