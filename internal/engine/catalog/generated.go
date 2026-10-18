package catalog

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Generation definitions have stable attribute identities, separate from defaults.
type ColumnGeneration struct {
	OID        ids.OID
	Kind       GenerationKind
	Expression expr.Expr
	// Retain analyzed syntax for dependencies/deparse and its physical plan for writes.
	Planned         expr.Expr
	Dependencies    []ids.ObjectRef
	CatalogSnapshot bool
}

type GenerationKind uint8

const (
	GenerationStored GenerationKind = iota + 1
	GenerationVirtual
)

func (k GenerationKind) SQL() string {
	if k == GenerationVirtual {
		return "VIRTUAL"
	}
	return "STORED"
}

func (d *Relation) IndexForAttribute(attribute int32) int {
	for slot, live := range d.Attributes {
		if live == attribute {
			return slot
		}
	}
	return -1
}
