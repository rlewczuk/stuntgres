package routine

import (
	"stuntgres/internal/engine/ids"
)

// IdentityDefaults is the number of defaulted trailing arguments of a builtin
// routine identity.
func IdentityDefaults(id ids.OID) uint8 {
	if declaration := BuiltinRoutine(id); declaration != nil {
		return declaration.Defaults
	}
	return 0
}
