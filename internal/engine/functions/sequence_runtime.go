package functions

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// SequenceRequest is one already-evaluated sequence call. Ref is the bound
// provider; without a domain, OID is the provider read from the argument.
// Value and Called are setval's inputs, converted by the engine after its
// authority checks, exactly where the engine node converted them.
type SequenceRequest struct {
	Name      string
	Ref       ids.ObjectRef
	OID       ids.OID
	Temporary ids.SessionToken
	Identity  bool
	Value     sqltype.Value
	Called    sqltype.Value
	HasCalled bool
}

// SequenceRuntime leaves statement authority, catalog epochs, locks, ACLs,
// read-only admission, generators and physical-session memories with the
// engine. SequenceAuthority runs before any argument is evaluated.
type SequenceRuntime interface {
	SequenceAuthority() error
	Sequence(SequenceRequest) (int64, error)
}
