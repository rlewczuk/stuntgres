package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type ColumnIdentity struct {
	Mode     parser.IdentityMode
	Sequence ids.ObjectAddress
}
