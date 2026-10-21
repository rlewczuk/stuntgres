package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// Engine.mu is held. Both ordinary and materialized views freeze the same bound
// identities and renderer metadata; only ordinary views execute this definition
// when read. A zero rule requests a new identity for a newly created relation.
func (e *Engine) freezeViewDefinitionLocked(auth AuthorizationView, syntax *parser.Select, b analyze.Binding, barrier, invoker bool, rule ids.OID, kind catalog.RelationKind, temporary bool) (*viewDefinition, error) {
	temporaryMessage := "temporary views are unsupported"
	if kind == catalog.MaterializedViewRelation {
		temporaryMessage = "materialized views must not use temporary tables or views"
	}
	temporaryConsumer := kind == catalog.ViewRelation && temporary
	if rule == 0 {
		var err error
		rule, err = e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
	}
	return stored.FreezeQueryPresentation(auth.Catalog, catalogSourceTemporaryOwner(auth), syntax, b, barrier, invoker, rule, temporaryMessage, temporaryConsumer)
}
