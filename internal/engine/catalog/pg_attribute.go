package catalog

import (
	"stuntgres/internal/sqltype"
)

// Scalar-only history survives removal of the original type and its dependency.
// Published maps and entries are immutable; no live query or codec sees them.
type AttributeMetadata struct {
	Name                                string
	TypeID                              uint32
	Number                              int32
	Facts                               sqltype.PostgreSQLAttributeFacts
	Typmod                              int32
	Dimensions                          int16
	Collation                           uint32
	NotNull, HasDefault, Dropped, Local bool
	Inheritance                         int64
	Identity, Generated                 string
	Compression                         string
}
