package core

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) OpenExtensionPaths(c *exec.Context, n plan.ExtensionPathsExpr, name string) ([]sqltype.Value, error) {
	scope := h.scope()
	if scope.Inquiry.CommentCatalog.State() == nil {
		return nil, fmt.Errorf("stuntgres: missing extension paths snapshot")
	}
	return extension.UpdatePathRows(c.Context, scope.Engine().Catalog.Extensions, name, n.Usage.Type)
}
