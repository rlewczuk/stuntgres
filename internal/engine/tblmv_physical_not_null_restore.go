package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type physicalNotNullConstructor struct {
	before    *catalog.Relation
	ref       ids.ObjectRef
	repairs   []physicalNotNullRepair
	indexName string
	deferred  *parser.UniqueConstraint
	competing bool
}

type restoredPhysicalNotNullFact struct {
	attribute int32
	oid       ids.OID
	usage     sqltype.Usage
}

// Look ahead only across the optional owner-to-superuser directive and a closed
// sequence of canonical cleanups. An ordinary ALTER receives no extra authority.
func physicalNotNullFollowing(statements []parser.Statement, position int) []physicalNotNullRepair {
	position++
	if position < len(statements) {
		if directive, ok := statements[position].(*parser.SessionAuthorization); ok && directive.OwnerDirective && !directive.Default {
			position++
		}
	}
	var repairs []physicalNotNullRepair
	for ; position < len(statements); position++ {
		deletion, ok := statements[position].(*parser.Delete)
		if !ok {
			break
		}
		repair, valid := restorePhysicalNotNullSyntax(deletion)
		if !valid {
			break
		}
		repairs = append(repairs, repair)
	}
	return repairs
}
