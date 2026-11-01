package inquiry

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/sqltype"
)

// partitionInquiry checks cancellation and returns the retained inquiry
// catalog. Nothing is held.
func partitionInquiry(h CatalogHost) (context.Context, catalog.Read, error) {
	ctx := h.Operation().Context()
	if err := ctx.Err(); err != nil {
		return nil, catalog.Read{}, err
	}
	view, err := h.InquiryCatalog()
	return ctx, view, err
}

func PartitionTree(h CatalogHost, oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	ctx, view, err := partitionInquiry(h)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := partition.TreeValues(ctx, view, oid, result)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(result, values), nil
}

func PartitionAncestors(h CatalogHost, oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	ctx, view, err := partitionInquiry(h)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := partition.AncestorValues(ctx, view, oid, result)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(result, values), nil
}

func PartitionRoot(h CatalogHost, oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	ctx, view, err := partitionInquiry(h)
	if err != nil {
		return sqltype.Value{}, err
	}
	return partition.RootValue(ctx, view, oid, result)
}
