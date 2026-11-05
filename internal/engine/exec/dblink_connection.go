package exec

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalDblinkConnection(n plan.InstalledDblinkConnectionExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed dblink reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	boundary := n.DblinkConnection
	boundary.RoutineReference = current
	if err := extension.ValidateDblinkConnectionCall(view, current, boundary); err != nil {
		return sqltype.Value{}, err
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return sqltype.Value{}, err
	}
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return sqltype.Value{}, err
	}
	var values [2]sqltype.Value
	null := false
	for i, child := range n.Children() {
		values[i], err = child.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		null = null || values[i].IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	body, err := n.EnterRoutine(c, values[:n.Count])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	n.Ref.Epoch = auth.Catalog.Epoch()
	n.Procedure, n.RoutineReference = current, current
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing dblink statement runtime")
	}
	return host.Contrib().DblinkConnection(n, values, auth.State.CurrentUser)
}
