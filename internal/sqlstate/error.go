package sqlstate

// Error is a PostgreSQL-compatible error with a five-character SQLSTATE.
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
