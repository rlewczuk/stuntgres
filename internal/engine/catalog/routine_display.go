package catalog

import (
	"slices"

	"stuntgres/internal/engine/ids"
)

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
	// Binary is the declaration's dynamic-library path, not a public-name
	// convention. HasBinary distinguishes SQL NULL from a present empty path.
	Binary         string
	HasBinary      bool
	Support        ids.OID
	Leakproof      bool
	TransformTypes *RoutineTransformTypes
	// OmitAllArgTypes preserves a released SQL NULL proallargtypes independently
	// of non-NULL argument modes. It does not alter formal/signature authority.
	OmitAllArgTypes bool
}

func (d RoutineDisplay) Valid() bool {
	return d.Language.String() != "" &&
		(d.HasBinary || d.Binary == "") &&
		(!d.HasBinary || d.Language == CRoutineLanguage)
}

// RoutineTransformTypes owns the immutable, ordered TRANSFORM FOR TYPE list.
// Its presence is independent of whether a transform execution provider exists.
type RoutineTransformTypes struct{ types []ids.OID }

func NewRoutineTransformTypes(types []ids.OID) *RoutineTransformTypes {
	if len(types) == 0 {
		return nil
	}
	return &RoutineTransformTypes{types: slices.Clone(types)}
}

func (t *RoutineTransformTypes) Len() int {
	if t == nil {
		return 0
	}
	return len(t.types)
}

func (t *RoutineTransformTypes) At(i int) ids.OID { return t.types[i] }
