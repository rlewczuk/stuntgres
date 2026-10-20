package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

var _ functions.CatalogInquiryRuntime = (*EvalContext)(nil)

func (c *EvalContext) CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	host := c.liveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing catalog role-name snapshot")
	}
	return host.CatalogUserName(oid, result)
}

func (h *statementHost) CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing catalog role-name snapshot")
	}
	engine := scope.op.session.Engine()
	if !scope.inquiry.commentRoles.BelongsTo(&engine.roles.domain) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign catalog role-name snapshot")
	}
	role, found := scope.inquiry.commentRoles.Find(engine.roles.domain.Principal(uint64(oid)))
	if found {
		return sqltype.String(result, role.Name), nil
	}
	return sqltype.String(result, "unknown (OID="+strconv.FormatUint(uint64(oid), 10)+")"), nil
}

func bareInquiryLiteralNull(node expr.Expr) bool {
	value, ok := node.(plan.LiteralExpr)
	return ok && value.Value.IsNull()
}
