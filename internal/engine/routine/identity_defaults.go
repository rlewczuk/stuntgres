package routine

import (
	"stuntgres/internal/engine/ids"
)

// Default counts absent from the native provider declaration inventory remain
// lookup-only metadata, never executable registrations. PostgreSQL REL_18_6:
// src/backend/catalog/system_functions.sql, pg_terminate_backend(pid integer,
// timeout int8 DEFAULT 0).
var builtinRoutineIdentityDefaults = map[ids.OID]uint8{2096: 1}

// IdentityDefaults is the number of defaulted trailing arguments of a builtin
// routine identity.
func IdentityDefaults(id ids.OID) uint8 {
	if declaration := BuiltinRoutine(id); declaration != nil {
		return declaration.Defaults
	}
	return builtinRoutineIdentityDefaults[id]
}
