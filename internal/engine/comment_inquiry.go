package engine

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) CommentInquiry(c *exec.Context, kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	if err := admitInquiryBody(c, provenance); err != nil {
		return sqltype.Value{}, err
	}
	if scope.Inquiry.CommentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing comment snapshot")
	}
	text, err := regobj.CommentText(scope.Inquiry.CommentCatalog, scope.Inquiry.commentRoles, kind, values)
	if err != nil {
		return sqltype.Value{}, err
	}
	if text == "" {
		return sqltype.Null(result), nil
	}
	return sqltype.String(result, text), nil
}
