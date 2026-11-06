package exec

import (
	"fmt"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (c *Context) EvalPgPrewarm(n plan.InstalledPgPrewarmExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil { return sqltype.Value{}, err }
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) { return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed pg_prewarm reference") }
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil { return sqltype.Value{}, err }
	boundary := n.PgPrewarm
	boundary.RoutineReference = current
	if err := extension.ValidatePgPrewarmCall(view, current, boundary); err != nil { return sqltype.Value{}, err }
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil { return sqltype.Value{}, err }
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil { return sqltype.Value{}, err }
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil { return sqltype.Value{}, err }
	if err := extension.ValidatePgPrewarmIdentity(auth.Catalog, current, auth.Ref(current.Address())); err != nil { return sqltype.Value{}, err }
	var values [5]sqltype.Value
	for i, child := range n.Args {
		value, err := child.Eval(c)
		if err != nil { return sqltype.Value{}, err }
		values[n.Slots[i]] = value
	}
	token, err := n.EnterRoutine(c, values[:])
	if err != nil { return sqltype.Value{}, err }
	defer token.Leave()
	for i, message := range [...]string{"relation cannot be null", "prewarm type cannot be null", "relation fork cannot be null"} {
		if values[i].IsNull() { return sqltype.Value{}, sqlError("22023", message) }
		if i == 1 {
			mode, err := values[i].Text()
			if err != nil { return sqltype.Value{}, err }
			if mode != "prefetch" && mode != "read" && mode != "buffer" { return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid prewarm type", Hint: "Valid prewarm types are \"prefetch\", \"read\", and \"buffer\"."} }
		}
	}
	fork, err := values[2].Text()
	if err != nil { return sqltype.Value{}, err }
	request := storage.PrewarmRequest{DefaultFirst: values[3].IsNull(), DefaultLast: values[4].IsNull()}
	switch fork {
	case "main": request.Fork = 0
	case "fsm": request.Fork = 1
	case "vm": request.Fork = 2
	case "init": request.Fork = 3
	default: return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "invalid fork name", Hint: "Valid fork names are \"main\", \"fsm\", \"vm\", and \"init\"."}
	}
	if !request.DefaultFirst { request.First, err = values[3].Integer(); if err != nil { return sqltype.Value{}, err } }
	if !request.DefaultLast { request.Last, err = values[4].Integer(); if err != nil { return sqltype.Value{}, err } }
	oid, err := values[0].ClassOID()
	if err != nil { return sqltype.Value{}, err }
	// Body admission reacquires current authority; argument values never retain it.
	auth, dependencies, err = RoutineInvocationAuthority(c)
	if err != nil { return sqltype.Value{}, err }
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil { return sqltype.Value{}, err }
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil { return sqltype.Value{}, err }
	n.Ref, n.Procedure, n.RoutineReference = auth.Ref(current.Address()), current, current
	host := c.LiveHost()
	if host == nil { return sqltype.Value{}, fmt.Errorf("stuntgres: missing pg_prewarm statement runtime") }
	count, err := host.Contrib().PgPrewarm(n, oid, request)
	if err != nil { return sqltype.Value{}, err }
	return sqltype.Integer(n.Usage.Type, count)
}
