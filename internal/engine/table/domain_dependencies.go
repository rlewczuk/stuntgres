package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type DomainDependency struct {
	Address, Target, Root ids.ObjectAddress
	OID                   ids.OID
	Ordinal               int32
	Label                 string
	Relation              *catalog.Relation
	Domain                *catalog.Type
	Column                string
	Automatic             bool
}
