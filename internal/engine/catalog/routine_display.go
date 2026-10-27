package catalog

// RoutineLanguage is declaration display metadata, not an execution capability
// or a pg_language address. Zero deliberately denotes missing provider facts.
type RoutineLanguage uint8

const (
	InternalRoutineLanguage RoutineLanguage = iota + 1
	SQLRoutineLanguage
	CRoutineLanguage
	PLpgSQLRoutineLanguage
	GoRoutineLanguage
)

func (l RoutineLanguage) String() string {
	switch l {
	case InternalRoutineLanguage:
		return "INTERNAL"
	case SQLRoutineLanguage:
		return "SQL"
	case CRoutineLanguage:
		return "C"
	case PLpgSQLRoutineLanguage:
		return "PLPGSQL"
	case GoRoutineLanguage:
		return "GO"
	}
	return ""
}

// RoutineDisplay is immutable, provider-owned declaration information. Source
// may legitimately be empty (SQL-standard bodies and native Go callbacks).
// Copy-on-write identity/ACL changes preserve this value; it grants no authority.
type RoutineDisplay struct {
	Language RoutineLanguage
	Source   string
}

func (d RoutineDisplay) Valid() bool { return d.Language.String() != "" }
