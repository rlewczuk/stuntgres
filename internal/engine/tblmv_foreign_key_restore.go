package engine

import (
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type restoredForeignKey struct {
	raw        parser.ForeignKeyConstraint
	path       []ids.ObjectAddress
	target     ids.ObjectAddress
	supporting ids.ObjectAddress // Exact captured primary for physical self-FK or omitted-reference inherited replay.
	inherited  *restoredInheritedForeignKey
	boundOID   ids.OID
	boundSlot  int // Actual append position in this sole stage, verified against boundOID.
}

type restoredForeignKeys struct {
	child       ids.ObjectRef
	auth        ddl.Authority // The declaring principal; root staging recovers it.
	definitions []restoredForeignKey
}

func stripRestoreForeignKeys(raw *parser.CreateTable) {
	elements := raw.Elements[:0]
	for _, element := range raw.Elements {
		if element.ForeignKey != nil {
			continue
		}
		if element.Column != nil {
			constraints := element.Column.Constraints[:0]
			for _, constraint := range element.Column.Constraints {
				if constraint.ForeignKey == nil {
					constraints = append(constraints, constraint)
				}
			}
			element.Column.Constraints = constraints
		}
		elements = append(elements, element)
	}
	raw.Elements = elements
}
