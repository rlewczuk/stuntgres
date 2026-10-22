package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// CatalogInquiryRuntime borrows captured statement authority for catalog-backed
// operations. Children are evaluated by their immutable owners before this seam.
type CatalogInquiryRuntime interface {
	CatalogUserName(uint32, *sqltype.Descriptor) (sqltype.Value, error)
	TablespaceLocation(uint32, *sqltype.Descriptor) (sqltype.Value, error)
	TablespaceDatabase(uint32, *sqltype.Descriptor) (sqltype.Value, bool, error)
	DeparseInquiry(DeparseSignature, [3]sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	CommentInquiry(CommentInquiryKind, [2]sqltype.Value, expr.Info, *sqltype.Descriptor) (sqltype.Value, error)
	VisibilityInquiry(VisibilityInquiryKind, sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	IndexProperty(IndexPropertyKind, uint32, sqltype.Value, string) (bool, bool, error)
	IdentifyObject([3]sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObjectAddress([3]sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	LookupObjectAddress([3]sqltype.Value) (ids.ObjectAddress, error)
	DescribeObject([3]sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
}

func catalogInquiryRuntime(c expr.Context) (CatalogInquiryRuntime, error) {
	runtime, ok := c.(CatalogInquiryRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing catalog inquiry runtime")
	}
	return runtime, nil
}
