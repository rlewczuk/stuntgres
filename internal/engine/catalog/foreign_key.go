package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// All links are stable catalog addresses/attributes, never descriptor pointers.
type ForeignKeyConstraint struct {
	OID              ids.OID
	Name             string
	Attributes       []int32
	Target           ids.ObjectAddress
	Referenced       []int32
	Index            ids.ObjectAddress
	Full             bool
	UpdateAction     parser.ForeignKeyAction
	DeleteAction     parser.ForeignKeyAction
	DeleteAttributes []int32
	NotValid         bool
}
