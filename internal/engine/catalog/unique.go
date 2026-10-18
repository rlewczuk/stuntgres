package catalog

import (
	"stuntgres/internal/engine/ids"
)

// Constraint and backing relation are separate identities published together.
type UniqueConstraint struct {
	OID              ids.OID
	Name             string
	Attributes       []int32
	Primary          bool
	NullsNotDistinct bool
	Index            ids.ObjectAddress
	Parent           ids.OID
	IsLocal          bool
}
