package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

type domainDependency struct {
	address, target, root ids.ObjectAddress
	oid                   ids.OID
	ordinal               int32
	label                 string
	relation              *catalog.Relation
	domain                *catalog.Type
	column                string
	automatic             bool
}
