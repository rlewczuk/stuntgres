package exec

import (
	"fmt"

	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalAmcheck(n plan.InstalledAmcheckExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed amcheck reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	boundary := n.Amcheck
	boundary.RoutineReference = current
	if err := extension.ValidateAmcheckCall(view, current, boundary); err != nil {
		return sqltype.Value{}, err
	}
	var values [4]sqltype.Value
	null := false
	for i, child := range n.Args {
		value, err := child.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return sqltype.Value{}, err
	}
	oid, err := values[0].ClassOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	var options [3]bool
	for i := 1; i < len(n.Args); i++ {
		options[i-1], err = values[i].Boolean()
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing amcheck statement runtime")
	}
	// Only the admitted lifetime can bridge a retained recipe into the current
	// catalog. Its preparation view can still carry the original epoch.
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	n.Ref.Epoch = auth.Catalog.Epoch()
	if err := extension.ValidateAmcheckIdentity(auth.Catalog, current, n.Ref); err != nil {
		return sqltype.Value{}, err
	}
	n.Procedure, n.RoutineReference = current, current
	if err := host.Amcheck(n, oid, options); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.VoidValue(n.Usage.Type), nil
}
