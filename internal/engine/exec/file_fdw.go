package exec

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalFileFDW(n plan.FileFDWExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil { return sqltype.Value{}, err }
	if err := n.ValidateStoredProvider(); err != nil { return sqltype.Value{}, err }
	captured := n.RoutineReference.(*catalog.Procedure)
	view := preparation.DomainCatalog(c)
	if !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) { return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed file_fdw reference") }
	current, err := ValidateStoredRoutineDescriptor(view, captured)
	if err != nil { return sqltype.Value{}, err }
	if err := extension.StockFileFDW.ValidateProcedure(view, current); err != nil { return sqltype.Value{}, err }
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil { return sqltype.Value{}, err }
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, captured); err != nil { return sqltype.Value{}, err }
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, captured)
	if err != nil { return sqltype.Value{}, err }
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return sqltype.Value{}, err }
	var values [2]sqltype.Value
	null := false
	for i, child := range n.Args {
		values[i], err = child.Eval(c)
		if err != nil { return sqltype.Value{}, err }
		null = null || values[i].IsNull()
	}
	if null { return sqltype.Null(n.Usage.Type), nil }
	body, err := n.EnterRoutine(c, values[:len(n.Args)])
	if err != nil { return sqltype.Value{}, err }
	defer body.Leave()
	if extension.StockFileFDW.SupportsHandler(current) {
		return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cannot display a value of type fdw_handler"}
	}
	host := c.LiveHost()
	if host == nil { return sqltype.Value{}, fmt.Errorf("stuntgres: missing file_fdw statement runtime") }
	n.RoutineReference = current
	if err := host.Contrib().FileFDWValidate(n, values); err != nil { return sqltype.Value{}, err }
	return sqltype.VoidValue(n.Usage.Type), nil
}
