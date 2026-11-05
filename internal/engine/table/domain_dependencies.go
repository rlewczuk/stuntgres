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
	Normal                bool // Independent NORMAL or reverse INTERNAL reach.
	// Via is the recorded dependent when Address is its INTERNAL owner, such
	// as a view's _RETURN rule. Discovery is ordered by it, not by Address.
	Via ids.ObjectAddress
}
