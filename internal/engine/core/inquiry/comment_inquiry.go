package inquiry

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func CommentInquiry(h CatalogHost, c *exec.Context, kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := h.AdmitInquiryBody(c, provenance); err != nil {
		return sqltype.Value{}, err
	}
	read, roles := h.CommentSnapshot()
	if read.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing comment snapshot")
	}
	text, err := regobj.CommentText(read, roles, kind, values)
	if err != nil {
		return sqltype.Value{}, err
	}
	if text == "" {
		return sqltype.Null(result), nil
	}
	return sqltype.String(result, text), nil
}
