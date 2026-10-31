package exec

import (
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalPgstattupleStats(n plan.InstalledPgstattupleStatsExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed pgstatindex reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	boundary := n.PgstattupleStats
	boundary.RoutineReference = current
	if err := extension.ValidatePgstattupleStatsCall(view, current, boundary); err != nil {
		return sqltype.Value{}, err
	}
	// EXECUTE precedes child evaluation and STRICT NULL, including dynamic NULL.
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
	value, err := n.Args[0].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing pgstatindex statement runtime")
	}
	integers, floats, err := host.PgstattupleStats(n, value)
	if err != nil {
		return sqltype.Value{}, err
	}
	d := n.Usage.Type
	integerCount, floatCount := 8, 2
	if n.Operation == functions.HashStats {
		integerCount, floatCount = 7, 1
	}
	fields := make([]sqltype.Value, integerCount+floatCount)
	for i, v := range integers[:integerCount] {
		fields[i], err = sqltype.Integer(d.CompositeMember(i).Usage.Type, v)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	for i, v := range floats[:floatCount] {
		fields[i+integerCount], err = sqltype.Float(d.CompositeMember(i+integerCount).Usage.Type, v)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
