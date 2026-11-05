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

func (c *Context) admitPgBuffercacheAggregate(n plan.InstalledPgBuffercacheAggregateExpr) error {
	if err := EvaluationContext(c); err != nil { return err }
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return fmt.Errorf("stuntgres: invalid installed pg_buffercache aggregate reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil { return err }
	boundary := n.PgBuffercacheAggregate
	boundary.RoutineReference = current
	if err := extension.ValidatePgBuffercacheAggregateCall(view, current, boundary); err != nil { return err }
	auth, dependencies, err := RoutineInvocationAuthority(c)
	if err != nil { return err }
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil { return err }
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return err }
	return auth.Check(access.Requirement{Object: auth.Ref(current.Address()), Privileges: authorization.ExecutePrivilege, RoutineACLOnly: true})
}
func (c *Context) capturePgBuffercacheAggregate(n plan.InstalledPgBuffercacheAggregateExpr) (functions.PgBuffercacheCounts, error) {
	body, err := c.FunctionEnterRoutine(n.RoutineReference, n.RoutineRecipeReference, expr.RoutineBodyInputs{Occurrence: n.Occurrence, Source: n.Source})
	if err != nil { return functions.PgBuffercacheCounts{}, err }
	defer body.Leave()
	auth, _, err := RoutineInvocationAuthority(c)
	if err != nil { return functions.PgBuffercacheCounts{}, err }
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil { return functions.PgBuffercacheCounts{}, err }
	n.Ref.Epoch, n.Procedure, n.RoutineReference = auth.Catalog.Epoch(), current, current
	if c.LiveHost() == nil { return functions.PgBuffercacheCounts{}, fmt.Errorf("stuntgres: missing pg_buffercache aggregate statement runtime") }
	return c.LiveHost().Contrib().PgBuffercacheAggregate(n)
}
func (c *Context) EvalPgBuffercacheAggregate(n plan.InstalledPgBuffercacheAggregateExpr) (sqltype.Value, error) {
	if err := c.admitPgBuffercacheAggregate(n); err != nil { return sqltype.Value{}, err }
	counts, err := c.capturePgBuffercacheAggregate(n)
	if err != nil { return sqltype.Value{}, err }
	return pgBuffercacheAggregateRecord(n.Usage.Type, &counts, n.Kind, 0)
}

// Open performs ordinary admission; the first reached advance enters the body.
// After that the cursor retains only a fixed detached observation and its type.
type pgBuffercacheAggregateSource struct {
	counts functions.PgBuffercacheCounts
	kind functions.PgBuffercacheAggregateKind
	captured bool
}
func (c *Context) OpenPgBuffercacheAggregate(n plan.InstalledPgBuffercacheAggregateExpr) (expr.SourceCursor, error) {
	if err := c.admitPgBuffercacheAggregate(n); err != nil { return expr.SourceCursor{}, err }
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, State: &pgBuffercacheAggregateSource{kind: n.Kind}, Advance: nextPgBuffercacheAggregate}), nil
}
func (c *Context) AdvancePgBuffercacheAggregate(n plan.InstalledPgBuffercacheAggregateExpr, ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || cursor.State == nil { return cursor.Next(ctx) }
	state, ok := cursor.State.(*pgBuffercacheAggregateSource)
	if !ok { cursor.Close(); return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid pg_buffercache aggregate source state") }
	if !state.captured {
		counts, err := c.capturePgBuffercacheAggregate(n)
		if err != nil { cursor.Close(); return sqltype.Value{}, false, err }
		state.counts, state.captured = counts, true
	}
	return cursor.Next(ctx)
}
func nextPgBuffercacheAggregate(ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if err := ctx.Err(); err != nil { return sqltype.Value{}, false, err }
	state := cursor.State.(*pgBuffercacheAggregateSource)
	if !state.captured { return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing pg_buffercache aggregate source authority") }
	limit := 1
	if state.kind.Set() { limit = 6 }
	if cursor.Position >= limit { return sqltype.Value{}, false, nil }
	value, err := pgBuffercacheAggregateRecord(cursor.Type, &state.counts, state.kind, cursor.Position)
	if err != nil { return sqltype.Value{}, false, err }
	cursor.Position++
	return value, true, nil
}
func pgBuffercacheAggregateRecord(shape *sqltype.Descriptor, counts *functions.PgBuffercacheCounts, kind functions.PgBuffercacheAggregateKind, bucket int) (sqltype.Value, error) {
	fields := make([]sqltype.Value, shape.CompositeLen())
	numbers := [4]int64{counts.Used, counts.Capacity-counts.Used, counts.Dirty, counts.Pinned}
	if kind.Set() {
		b := counts.Buckets[bucket]
		numbers = [4]int64{int64(bucket), b.Buffers, b.Dirty, b.Pinned}
	}
	for i, number := range numbers {
		value, err := sqltype.Integer(shape.CompositeMember(i).Usage.Type, number)
		if err != nil { return sqltype.Value{}, err }
		fields[i] = value
	}
	if !kind.Set() {
		typ := shape.CompositeMember(4).Usage.Type
		fields[4] = sqltype.Null(typ)
		if counts.Used != 0 {
			value, err := sqltype.Float(typ, float64(counts.UsageTotal)/float64(counts.Used))
			if err != nil { return sqltype.Value{}, err }
			fields[4] = value
		}
	}
	return sqltype.OwnedCompositeValue(shape, fields)
}
