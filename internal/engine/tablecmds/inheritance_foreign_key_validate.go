package tablecmds

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
)

// Comments and final attribute-rank repairs do not alter these key/provider
// observations. No later key, owner, type or provider incarnation can reuse them.
func validateRestoredInheritedForeignKey(e *core.Engine, receipt *table.RestoredInheritedForeignKey, op *session.Operation, declaration table.RestoredForeignKeys, view catalog.Read) error {
	auth := core.DDLAuthorityOf(declaration.Auth)
	auth.Catalog = view
	auth.Recapture(view)
	if receipt.Source != declaration.Child || receipt.Source.Domain() != e.Catalog.Domain || receipt.Target.Domain() != e.Catalog.Domain || receipt.Source.Epoch != view.Epoch() || receipt.Target.Epoch != view.Epoch() || auth.RoleRecord != e.RoleStore.Published || !core.PartitionConstraintReplayImport(e, op, auth) {
		return ids.ErrCatalogReplaced
	}
	if !receipt.Unchanged(view) {
		return ids.ErrCatalogReplaced
	}
	return auth.Check(access.Requirement{Object: receipt.Source, OwnerOnly: true})
}
