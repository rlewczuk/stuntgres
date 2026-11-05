package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func (c *Context) pgBuffercacheEvictionArguments(n plan.InstalledPgBuffercacheEvictionExpr) (sqltype.Value, bool, error) {
	if err := EvaluationContext(c); err != nil { return sqltype.Value{}, false, err }
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid installed pg_buffercache eviction reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil { return sqltype.Value{}, false, err }
	boundary := n.PgBuffercacheEviction
	boundary.RoutineReference = current
	if err := extension.ValidatePgBuffercacheEvictionCall(view, current, boundary); err != nil { return sqltype.Value{}, false, err }
	// SQL admission remains ordinary even when strictness suppresses the body.
	auth, dependencies, err := RoutineInvocationAuthority(c)
	if err != nil { return sqltype.Value{}, false, err }
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil { return sqltype.Value{}, false, err }
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return sqltype.Value{}, false, err }
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Address()), Privileges: authorization.ExecutePrivilege, RoutineACLOnly: true}); err != nil { return sqltype.Value{}, false, err }
	if len(n.Args) == 0 { return sqltype.Value{}, false, nil }
	value, err := n.Args[0].Eval(c)
	return value, value.IsNull(), err
}
func (c *Context) EvalPgBuffercacheEviction(n plan.InstalledPgBuffercacheEvictionExpr) (sqltype.Value, error) {
	value, null, err := c.pgBuffercacheEvictionArguments(n)
	if err != nil { return sqltype.Value{}, err }
	return c.evalPgBuffercacheEviction(n, value, null)
}
func (c *Context) evalPgBuffercacheEviction(n plan.InstalledPgBuffercacheEvictionExpr, value sqltype.Value, null bool) (sqltype.Value, error) {
	if null { return sqltype.Null(n.Usage.Type), nil }
	facts := expr.RoutineBodyInputs{Count: len(n.Args), Occurrence: n.Occurrence, Source: n.Source}
	if len(n.Args) != 0 { facts.First = n.Args[0].ResultUsage().Type }
	body, err := c.FunctionEnterRoutine(n.RoutineReference, n.RoutineRecipeReference, facts)
	if err != nil { return sqltype.Value{}, err }
	defer body.Leave()
	auth, _, err := RoutineInvocationAuthority(c)
	if err != nil { return sqltype.Value{}, err }
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil { return sqltype.Value{}, err }
	n.Ref.Epoch, n.Procedure, n.RoutineReference = auth.Catalog.Epoch(), current, current
	var input int64
	if n.Kind == functions.EvictRelation {
		oid, err := value.ClassOID()
		if err != nil { return sqltype.Value{}, err }
		input = int64(oid)
	} else if n.Kind == functions.EvictBuffer {
		input, err = value.Integer()
		if err != nil { return sqltype.Value{}, err }
	}
	if c.LiveHost() == nil { return sqltype.Value{}, fmt.Errorf("stuntgres: missing pg_buffercache eviction statement runtime") }
	counts, err := c.LiveHost().Contrib().PgBuffercacheEviction(n, input)
	if err != nil { return sqltype.Value{}, err }
	fields := make([]sqltype.Value, n.Usage.Type.CompositeLen())
	for i := range fields {
		typ := n.Usage.Type.CompositeMember(i).Usage.Type
		if n.Kind == functions.EvictBuffer {
			fields[i] = sqltype.Boolean(typ, counts[i] != 0)
		} else {
			fields[i], err = sqltype.Integer(typ, counts[i])
			if err != nil { return sqltype.Value{}, err }
		}
	}
	return sqltype.OwnedCompositeValue(n.Usage.Type, fields)
}

type pgBuffercacheEvictionSource struct { value sqltype.Value; null bool }
func (c *Context) OpenPgBuffercacheEviction(n plan.InstalledPgBuffercacheEvictionExpr) (expr.SourceCursor, error) {
	value, null, err := c.pgBuffercacheEvictionArguments(n)
	if err != nil { return expr.SourceCursor{}, err }
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, State: &pgBuffercacheEvictionSource{value: value, null: null}, Advance: nextPgBuffercacheEviction}), nil
}
func (c *Context) AdvancePgBuffercacheEviction(n plan.InstalledPgBuffercacheEvictionExpr, ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || cursor.State == nil { return cursor.Next(ctx) }
	state, ok := cursor.State.(*pgBuffercacheEvictionSource)
	if !ok { cursor.Close(); return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid pg_buffercache eviction source state") }
	value, err := c.evalPgBuffercacheEviction(n, state.value, state.null)
	if err != nil { cursor.Close(); return sqltype.Value{}, false, err }
	*cursor = expr.ScalarSource(value)
	return cursor.Next(ctx)
}

func nextPgBuffercacheEviction(context.Context, *expr.SourceCursor) (sqltype.Value, bool, error) {
	return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing pg_buffercache eviction source authority")
}
