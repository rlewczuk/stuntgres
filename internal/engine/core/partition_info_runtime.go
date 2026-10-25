package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqltype"
)

// partitionInquiry checks cancellation and returns the retained inquiry
// catalog. Nothing is held.
func (h *statementHost) partitionInquiry() (context.Context, catalog.Read, error) {
	ctx := h.scope().Op.Context()
	if err := ctx.Err(); err != nil {
		return nil, catalog.Read{}, err
	}
	view, err := h.catalogInquiryCatalog()
	return ctx, view, err
}

func (h *statementHost) PartitionTree(oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	ctx, view, err := h.partitionInquiry()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := partition.TreeValues(ctx, view, oid, result)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(result, values), nil
}

func (h *statementHost) PartitionAncestors(oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	ctx, view, err := h.partitionInquiry()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := partition.AncestorValues(ctx, view, oid, result)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(result, values), nil
}

func (h *statementHost) PartitionRoot(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	ctx, view, err := h.partitionInquiry()
	if err != nil {
		return sqltype.Value{}, err
	}
	return partition.RootValue(ctx, view, oid, result)
}
