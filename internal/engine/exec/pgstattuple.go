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

func (c *Context) EvalPgstattuple(n plan.InstalledPgstattupleExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil { return sqltype.Value{}, err }
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed pgstattuple reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil { return sqltype.Value{}, err }
	boundary := n.Pgstattuple
	boundary.RoutineReference = current
	if err := extension.ValidatePgstattupleCall(view, current, boundary); err != nil { return sqltype.Value{}, err }
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil { return sqltype.Value{}, err }
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil { return sqltype.Value{}, err }
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil { return sqltype.Value{}, err }
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return sqltype.Value{}, err }
	n.Ref.Epoch = auth.Catalog.Epoch()
	n.Procedure, n.RoutineReference = current, current
	value, err := n.Args[0].Eval(c)
	if err != nil { return sqltype.Value{}, err }
	if value.IsNull() { return sqltype.Null(n.Usage.Type), nil }
	host := c.LiveHost()
	if host == nil { return sqltype.Value{}, fmt.Errorf("stuntgres: missing pgstattuple statement runtime") }
	stats, err := host.Pgstattuple(n, value)
	if err != nil { return sqltype.Value{}, err }
	d := n.Usage.Type
	if n.Kind == functions.HeapStatisticsPages { return sqltype.Integer(d, stats.TableLen/8192) }
	integers := [...]int64{stats.TableLen, stats.TupleCount, stats.TupleLen, stats.DeadCount, stats.DeadLen, stats.FreeSpace}
	floats := [...]float64{stats.TuplePercent, stats.DeadPercent, stats.FreePercent}
	fields := make([]sqltype.Value, d.CompositeLen())
	intSlots := [...]int{0,1,2,4,5,7}
	floatSlots := [...]int{3,6,8}
	if n.Kind == functions.HeapStatisticsApprox {
		intSlots = [...]int{0,2,3,5,6,8}
		floatSlots = [...]int{4,7,9}
		fields[1], err = sqltype.Float(d.CompositeMember(1).Usage.Type, stats.ScannedPercent)
		if err != nil { return sqltype.Value{}, err }
	}
	for i, slot := range intSlots {
		fields[slot], err = sqltype.Integer(d.CompositeMember(slot).Usage.Type, integers[i])
		if err != nil { return sqltype.Value{}, err }
	}
	for i, slot := range floatSlots {
		fields[slot], err = sqltype.Float(d.CompositeMember(slot).Usage.Type, floats[i])
		if err != nil { return sqltype.Value{}, err }
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
