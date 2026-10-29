package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type ExclusionConstraint struct {
	OID     ids.OID
	Name    string
	Index   ids.ObjectAddress
	Parent  ids.OID
	IsLocal bool
	// NoInherit records declaration provenance independently of attachment.
	NoInherit                     bool
	Operators                     []sqltype.ExclusionOperator
	Deferrable, InitiallyDeferred bool
}
