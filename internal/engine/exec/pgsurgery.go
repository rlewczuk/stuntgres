package exec

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalPgsurgery(n plan.InstalledPgsurgeryExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed pg_surgery reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	boundary := n.Pgsurgery
	boundary.RoutineReference = current
	if err := extension.ValidatePgsurgeryCall(view, current, boundary); err != nil {
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
	n.Ref.Epoch = auth.Catalog.Epoch()
	n.Procedure, n.RoutineReference = current, current
	var values [2]sqltype.Value
	null := false
	for slot := range values {
		i := slot
		if n.Slots[0] == 1 {
			i = 1 - slot
		}
		value, err := n.Args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	oid, err := values[0].ClassOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing pg_surgery statement runtime")
	}
	if err := host.Contrib().Pgsurgery(n, oid, values[1]); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.VoidValue(n.Usage.Type), nil
}
