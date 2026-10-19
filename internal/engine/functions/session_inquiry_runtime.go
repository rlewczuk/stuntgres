package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// SessionInquiryRuntime borrows the executing statement's identity and physical
// session authority. Immutable expressions never retain that authority.
type SessionInquiryRuntime interface {
	IdentityValue(IdentityKind, *sqltype.Descriptor) (sqltype.Value, error)
	MyTempNamespace() (uint32, error)
	OtherTempNamespace(uint32) (bool, error)
	CurrentSchemas(bool, *sqltype.Descriptor) (sqltype.Value, error)
}

func sessionInquiryRuntime(c expr.Context) (SessionInquiryRuntime, error) {
	runtime, ok := c.(SessionInquiryRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing session inquiry runtime")
	}
	return runtime, nil
}
