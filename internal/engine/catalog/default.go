package catalog

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Published definitions contain immutable typed nodes, never evaluated values
// or caller-owned syntax. Attribute keys survive descriptor replacements.
type ColumnDefault struct {
	OID                    ids.OID
	Expression             expr.Expr
	Dependencies           []ids.ObjectRef
	CatalogSnapshot        bool
	CatalogAliasReferences bool
}
