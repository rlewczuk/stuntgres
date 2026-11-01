package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func SizeInquiry(h CatalogHost, kind functions.SizeKind, arg sqltype.Value, fork string, result *sqltype.Descriptor) (sqltype.Value, error) {
	ctx := h.Operation().Context()
	if err := ctx.Err(); err != nil { return sqltype.Value{}, err }
	h.ReacquireEngine()
	captures, present, err := captureSizeLocked(h, ctx, kind, arg, fork)
	h.ReleaseEngineLocked()
	if err != nil { return sqltype.Value{}, err }
	if !present { return sqltype.Null(result), nil }
	var sum int64
	for _, capture := range captures {
		components, err := capture.Forks(ctx)
		if err != nil { return sqltype.Value{}, err }
		var n int64
		if kind == functions.RelationSize {
			switch fork { case "main": n = components.Main; case "fsm": n = components.FSM; case "vm": n = components.VM; case "init": n = components.Init }
		} else {
			n, err = components.Total()
			if err != nil { return sqltype.Value{}, err }
		}
		sum, err = storage.AddSize(sum, n)
		if err != nil { return sqltype.Value{}, err }
	}
	return sqltype.Integer(result, sum)
}

// captureSizeLocked captures only headers, descriptors and immutable roots. The
// shared cluster mutex owns all runtime maps; arithmetic runs after its release.
func captureSizeLocked(h CatalogHost, ctx context.Context, kind functions.SizeKind, arg sqltype.Value, fork string) ([]storage.Allocation, bool, error) {
	retained, err := h.InquiryCatalog()
	if err != nil { return nil, false, err }
	if retained.Epoch() != h.CatalogEpochLocked() { return nil, false, ids.ErrCatalogReplaced }
	view := h.PrivateCatalogLocked(h.PendingCatalog())
	if kind == functions.DatabaseSize || kind == functions.TablespaceSize {
		return h.AggregateAllocationsLocked(ctx, kind, arg, view)
	}
	oid, err := arg.ClassOID()
	if err != nil { return nil, false, err }
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	relation, found := view.Relation(address)
	if !found { return nil, false, nil }
	if kind == functions.RelationSize && fork != "main" && fork != "fsm" && fork != "vm" && fork != "init" {
		return nil, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid fork name", Hint: `Valid fork names are "main", "fsm", "vm", and "init".`}
	}
	var captures []storage.Allocation
	if kind != functions.IndexesSize {
		captures = append(captures, h.RelationAllocationLocked(view, relation))
	}
	if kind == functions.IndexesSize || kind == functions.TotalRelationSize {
		for _, descriptor := range view.Descriptors() {
			if err := ctx.Err(); err != nil { return nil, false, err }
			idx, ok := descriptor.(*catalog.Relation)
			if ok && idx.SQLIndex != nil && idx.SQLIndex.Parent == address {
				captures = append(captures, h.RelationAllocationLocked(view, idx))
			}
		}
	}
	return captures, true, nil
}
