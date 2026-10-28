package functions

import "stuntgres/internal/sqltype"

// SessionActivityRuntime borrows only the currently admitted frontend command.
// Prepared recipes and cursor snapshots never own this capability.
type SessionActivityRuntime interface {
	SessionActivityValue(SessionActivityKind, *sqltype.Descriptor) (sqltype.Value, error)
}
