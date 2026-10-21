package view

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// FreezeDefinition freezes a view query read through catalogView, with
// owner's temporary objects. Both ordinary and materialized views freeze the
// same bound identities and renderer metadata; only ordinary views execute
// this definition when read. A zero rule allocates a new identity for a newly
// created relation.
func FreezeDefinition(a ddl.Allocator, catalogView catalog.Read, owner ids.SessionToken, syntax *parser.Select, b analyze.Binding, barrier, invoker bool, rule ids.OID, kind catalog.RelationKind, temporary bool) (*stored.View, error) {
	temporaryMessage := "temporary views are unsupported"
	if kind == catalog.MaterializedViewRelation {
		temporaryMessage = "materialized views must not use temporary tables or views"
	}
	temporaryConsumer := kind == catalog.ViewRelation && temporary
	if rule == 0 {
		var err error
		rule, err = a.AllocateOID()
		if err != nil {
			return nil, err
		}
	}
	return stored.FreezeQueryPresentation(catalogView, owner, syntax, b, barrier, invoker, rule, temporaryMessage, temporaryConsumer)
}
