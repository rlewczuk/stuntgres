package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (c *Context) pgvisibilityArguments(n plan.InstalledPgvisibilityExpr) (values [2]sqltype.Value, null bool, err error) {
	if err = EvaluationContext(c); err != nil {
		return
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		err = fmt.Errorf("stuntgres: invalid installed pg_visibility reference")
		return
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return
	}
	boundary := n.Pgvisibility
	boundary.RoutineReference = current
	if err = extension.ValidatePgvisibilityCall(view, current, boundary); err != nil {
		return
	}
	for i, child := range n.Args {
		value, failure := child.Eval(c)
		if failure != nil {
			err = failure
			return
		}
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	return
}

func (c *Context) pgvisibilityInvocation(n *plan.InstalledPgvisibilityExpr, values [2]sqltype.Value) (uint32, int64, error) {
	auth, dependencies, err := RoutineInvocationAuthority(c)
	if err != nil {
		return 0, 0, err
	}
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return 0, 0, err
	}
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return 0, 0, err
	}
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return 0, 0, err
	}
	n.Ref.Epoch = auth.Catalog.Epoch()
	n.Procedure, n.RoutineReference = current, current
	oid, err := values[0].ClassOID()
	if err != nil {
		return 0, 0, err
	}
	var block int64
	if n.Kind.Block() {
		block, err = values[1].Integer()
		if err != nil {
			return 0, 0, err
		}
	}
	if c.LiveHost() == nil {
		return 0, 0, fmt.Errorf("stuntgres: missing pg_visibility statement runtime")
	}
	return oid, block, nil
}
func pgvisibilityRecord(d *sqltype.Descriptor, kind functions.PgvisibilityKind, counts [2]int64, block int64, bits storage.VisibilityBits, pd bool) (sqltype.Value, error) {
	fields := make([]sqltype.Value, d.CompositeLen())
	if kind == functions.VisibilityMapSummary {
		var err error
		fields[0], err = sqltype.Integer(d.CompositeMember(0).Usage.Type, counts[0])
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[1], err = sqltype.Integer(d.CompositeMember(1).Usage.Type, counts[1])
		if err != nil {
			return sqltype.Value{}, err
		}
	} else {
		offset := 0
		if kind.Set() {
			var err error
			fields[0], err = sqltype.Integer(d.CompositeMember(0).Usage.Type, block)
			if err != nil {
				return sqltype.Value{}, err
			}
			offset = 1
		}
		fields[offset] = sqltype.Boolean(d.CompositeMember(offset).Usage.Type, bits.AllVisible)
		fields[offset+1] = sqltype.Boolean(d.CompositeMember(offset+1).Usage.Type, bits.AllFrozen)
		if kind.Page() {
			fields[offset+2] = sqltype.Boolean(d.CompositeMember(offset+2).Usage.Type, pd)
		}
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
func (c *Context) EvalPgvisibility(n plan.InstalledPgvisibilityExpr) (sqltype.Value, error) {
	if n.Kind.Set() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: pg_visibility set requires source invocation")
	}
	values, null, err := c.pgvisibilityArguments(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	return c.evalPgvisibilityValues(n, values, null)
}

// The scalar expression and scalar FROM routes share the same kernel. The
// latter borrows its evaluator only on demand, without evaluating inputs twice.
func (c *Context) evalPgvisibilityValues(n plan.InstalledPgvisibilityExpr, values [2]sqltype.Value, null bool) (sqltype.Value, error) {
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	body, err := c.enterPgvisibility(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	oid, block, err := c.pgvisibilityInvocation(&n, values)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind == functions.VisibilityTruncateMap {
		if err := c.LiveHost().Contrib().PgvisibilityTruncate(n, oid); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.VoidValue(n.Usage.Type), nil
	}
	result, err := c.LiveHost().Contrib().Pgvisibility(n, oid, block)
	if err != nil {
		return sqltype.Value{}, err
	}
	var bits storage.VisibilityBits
	if n.Kind != functions.VisibilityMapSummary {
		bits = result.Probe(block)
	}
	pd := bits.AllVisible
	if result.PageRuns != nil {
		pd = result.PageVisible(block)
	}
	return pgvisibilityRecord(n.Usage.Type, n.Kind, [2]int64{result.AllVisible, result.AllFrozen}, block, bits, pd)
}

func (c *Context) enterPgvisibility(n plan.InstalledPgvisibilityExpr) (sqltype.RoutineBodyToken, error) {
	facts := expr.RoutineBodyInputs{Count: len(n.Args), Occurrence: n.Occurrence, Source: n.Source}
	if len(n.Args) > 0 {
		facts.First = n.Args[0].ResultUsage().Type
	}
	if len(n.Args) > 1 {
		facts.Second = n.Args[1].ResultUsage().Type
	}
	return c.FunctionEnterRoutine(n.RoutineReference, n.RoutineRecipeReference, facts)
}

type visibilitySource struct {
	values       [2]sqltype.Value
	result       storage.VisibilityMap
	findings     []ids.VersionID
	kind         functions.PgvisibilityKind
	initialized  bool
	run, pageRun int
}

func (c *Context) OpenPgvisibility(n plan.InstalledPgvisibilityExpr) (expr.SourceCursor, error) {
	values, null, err := c.pgvisibilityArguments(n)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if null {
		if !n.Kind.Set() {
			return expr.ScalarSource(sqltype.Null(n.Usage.Type)), nil
		}
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, State: &visibilitySource{values: values, kind: n.Kind}, Advance: nextVisibilitySource}), nil
}

func (c *Context) AdvancePgvisibility(n plan.InstalledPgvisibilityExpr, ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || cursor.State == nil {
		return cursor.Next(ctx)
	}
	state, ok := cursor.State.(*visibilitySource)
	if !ok || state.kind != n.Kind {
		cursor.Close()
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid visibility source state")
	}
	if !n.Kind.Set() {
		value, err := c.evalPgvisibilityValues(n, state.values, false)
		if err != nil {
			cursor.Close()
			return sqltype.Value{}, false, err
		}
		*cursor = expr.ScalarSource(value)
		return cursor.Next(ctx)
	}
	body, err := c.enterPgvisibility(n)
	if err != nil {
		cursor.Close()
		return sqltype.Value{}, false, err
	}
	defer body.Leave()
	if !state.initialized {
		oid, block, err := c.pgvisibilityInvocation(&n, state.values)
		if err == nil {
			if n.Kind.Check() {
				state.findings, err = c.LiveHost().Contrib().PgvisibilityCheck(n, oid)
			} else {
				state.result, err = c.LiveHost().Contrib().Pgvisibility(n, oid, block)
				if err == nil && state.result.Blocks > int64(^uint(0)>>1) {
					err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "bigint out of range"}
				}
			}
		}
		if err != nil {
			cursor.Close()
			return sqltype.Value{}, false, err
		}
		state.values = [2]sqltype.Value{}
		state.initialized = true
	}
	return cursor.Next(ctx)
}

func nextVisibilitySource(ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	state, ok := cursor.State.(*visibilitySource)
	if !ok || !state.initialized {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing visibility source authority")
	}
	if state.kind.Check() {
		if cursor.Position >= len(state.findings) {
			return sqltype.Value{}, false, nil
		}
		block, offset, err := storage.LogicalTID(state.findings[cursor.Position])
		if err != nil {
			return sqltype.Value{}, false, err
		}
		value, err := sqltype.TIDValue(cursor.Type, block, offset)
		cursor.Position++
		return value, err == nil, err
	}
	block := cursor.Current
	if block >= state.result.Blocks {
		return sqltype.Value{}, false, nil
	}
	for state.run < len(state.result.Runs) && block >= state.result.Runs[state.run].End {
		state.run++
	}
	if state.run == len(state.result.Runs) {
		return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid visibility map runs")
	}
	bits := state.result.Runs[state.run].Bits
	pd := bits.AllVisible
	if state.result.PageRuns != nil {
		for state.pageRun < len(state.result.PageRuns) && block >= state.result.PageRuns[state.pageRun].End {
			state.pageRun++
		}
		if state.pageRun == len(state.result.PageRuns) {
			return sqltype.Value{}, false, fmt.Errorf("stuntgres: invalid visibility page runs")
		}
		pd = state.result.PageRuns[state.pageRun].Bits.AllVisible
	}
	value, err := pgvisibilityRecord(cursor.Type, state.kind, [2]int64{}, block, bits, pd)
	cursor.Current++
	return value, err == nil, err
}
