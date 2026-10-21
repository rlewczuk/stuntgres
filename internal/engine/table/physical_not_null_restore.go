package table

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type PhysicalNotNullConstructor struct {
	Before    *catalog.Relation
	Ref       ids.ObjectRef
	Repairs   []PhysicalNotNullRepair
	IndexName string
	Deferred  *parser.UniqueConstraint
	Competing bool
}

type RestoredPhysicalNotNullFact struct {
	Attribute int32
	OID       ids.OID
	Usage     sqltype.Usage
}

// Look ahead only across the optional owner-to-superuser directive and a closed
// sequence of canonical cleanups. An ordinary ALTER receives no extra authority.
func PhysicalNotNullFollowing(statements []parser.Statement, position int) []PhysicalNotNullRepair {
	position++
	if position < len(statements) {
		if directive, ok := statements[position].(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default {
			position++
		}
	}
	var repairs []PhysicalNotNullRepair
	for ; position < len(statements); position++ {
		deletion, ok := statements[position].(*parser.Delete)
		if !ok {
			break
		}
		repair, valid := RestorePhysicalNotNullSyntax(deletion)
		if !valid {
			break
		}
		repairs = append(repairs, repair)
	}
	return repairs
}
