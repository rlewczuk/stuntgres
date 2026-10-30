package functions

import "stuntgres/internal/sqltype"

// ServerSignalRuntime is borrowed from the executing statement, never prepared state.
type ServerSignalRuntime interface {
 ServerSignalValue(ServerSignalKind, int64, *sqltype.Descriptor) (sqltype.Value, error)
}
