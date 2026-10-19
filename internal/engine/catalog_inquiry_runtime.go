package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

var _ functions.CatalogInquiryRuntime = (*EvalContext)(nil)

func (c *EvalContext) CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := c.liveScope()
	if scope == nil || scope.commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing catalog role-name snapshot")
	}
	engine := scope.op.session.Engine()
	if !scope.commentRoles.BelongsTo(&engine.roles.domain) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: foreign catalog role-name snapshot")
	}
	role, found := scope.commentRoles.Find(engine.roles.domain.Principal(uint64(oid)))
	if found {
		return sqltype.String(result, role.Name), nil
	}
	return sqltype.String(result, "unknown (OID="+strconv.FormatUint(uint64(oid), 10)+")"), nil
}

func bareInquiryLiteralNull(node expr.Expr) bool {
	value, ok := node.(literalExpression)
	return ok && value.value.IsNull()
}
