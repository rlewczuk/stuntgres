package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

type sqlRoutineExpression struct {
	expr.Info
	procedure *catalog.Procedure
	ref       ids.ObjectRef
	args      []expr.Expr
}

func (n sqlRoutineExpression) Children() []expr.Expr { return n.args }
func (n sqlRoutineExpression) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Args: n.args, Count: len(n.args), SelectedImplicit: true}, true, nil
}
func (n sqlRoutineExpression) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if current, ok := info.RoutineReference.(*catalog.Procedure); ok {
		n.procedure = current
	}
	if n.procedure == nil || n.procedure.SQL == nil || len(children) != len(n.procedure.SQL.Inputs) {
		return nil, fmt.Errorf("invalid SQL routine provider")
	}
	for i, child := range children {
		if child == nil || child.ResultUsage().Type == nil || child.ResultUsage().Type.ID() != n.procedure.SQL.Inputs[i].Type.ID() {
			return nil, fmt.Errorf("invalid SQL routine argument")
		}
	}
	n.Info, n.args = info, children
	return n, nil
}
func (n sqlRoutineExpression) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, _ bool) bool {
	o, ok := other.(sqlRoutineExpression)
	if !ok || n.ref != o.ref || n.procedure.SQL != o.procedure.SQL || len(n.args) != len(o.args) {
		return false
	}
	for i, arg := range n.args {
		if !same(arg, o.args[i]) {
			return false
		}
	}
	return true
}
func (n sqlRoutineExpression) ValidateStoredProvider() error {
	if n.procedure == nil || n.procedure.SQL == nil || n.Info.RoutineReference != n.procedure || n.ref == (ids.ObjectRef{}) || n.ref.Address != n.procedure.Address() || n.Usage.Type == nil || !n.Usage.Valid() || n.Usage.Type.ID() != sqltype.ID(n.procedure.Result) {
		return fmt.Errorf("invalid stored SQL routine provider")
	}
	if _, err := n.Rebuild(n.Info, n.args); err != nil {
		return err
	}
	want := expr.Properties{Volatility: expr.Volatile}
	for _, arg := range n.args {
		want = expr.MergeProperties(want, arg.Properties())
	}
	if n.Property != want {
		return fmt.Errorf("invalid stored SQL routine properties")
	}
	return nil
}
func (sqlRoutineExpression) IntrinsicVolatility() expr.Volatility { return expr.Volatile }
func (sqlRoutineExpression) PlanningFold() expr.Fold              { return expr.FoldNever }
func (n sqlRoutineExpression) Eval(context expr.Context) (value sqltype.Value, err error) {
	c := evalContext(context)
	if err = evaluationContext(c); err != nil {
		return value, err
	}
	frame := proc.NewFrame(n.procedure.SQL.Inputs)
	defer frame.Close()
	for i, arg := range n.args {
		v, err := arg.Eval(c)
		if err != nil {
			return value, err
		}
		if err = frame.Bind(i, v, arg.ResultUsage()); err != nil {
			return value, err
		}
	}
	scope := c.statementScope
	if scope == nil && c.query != nil && c.query.source != nil {
		scope = c.query.source.liveScope()
	}
	if scope == nil {
		return value, fmt.Errorf("SQL routine invocation has no statement owner")
	}
	value, err = scope.invokeSQLRoutine(n.procedure, frame, n.Usage)
	if err != nil {
		return value, sqlRoutineContext(err, n.procedure.Name(), true)
	}
	return frame.Result(value, n.Usage)
}

type sqlRoutineAnalysis struct {
	definition  *proc.Definition
	description StatementDescription
}

// invokeSQLRoutine borrows the admitted operation/transaction. It never acquires
// a driver connection, starts a root command, calls Finish or ends the operation.
func (parent *StatementScope) invokeSQLRoutine(d *catalog.Procedure, frame *proc.Frame, resultUsage sqltype.Usage) (value sqltype.Value, err error) {
	if err := parent.enterSQLRoutine(); err != nil {
		return value, err
	}
	defer parent.leaveSQLRoutine()
	e := parent.op.session.Engine()
	restore := parent.activateSQLRoutine(d.SQL.Inputs)
	defer func() { err = restore(err) }()
	parent.sqlRoutineResult = resultUsage
	child := parent
	e.mu.Lock()
	auth, err := e.authorizationLocked(parent.op)
	if err == nil {
		_, err = validateRoutineDescriptor(auth.Catalog, auth.Roles, d)
		if err == nil {
			err = auth.Check(access.Requirement{Object: e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address()), Privileges: authorization.ExecutePrivilege})
		}
	}
	if err != nil {
		e.mu.Unlock()
		return value, err
	}
	if retained, ok := parent.op.session.sqlRoutines[d.Address()]; ok && retained.definition == d.SQL {
		description := retained.description
		child.Expected = &description
	}
	e.mu.Unlock()
	if err = child.selectPreparedAnalysis(d.SQL.Query); err != nil {
		return value, err
	}
	if err = child.acquireRelation(d.SQL.Query, true); err != nil {
		return value, err
	}
	if child.Expected == nil {
		description, describeErr := e.describeOperation(child, d.SQL.Query)
		if describeErr != nil {
			return value, describeErr
		}
		child.Expected = &description
	}
	result, err := e.executeStatement(child, d.SQL.Query, len(frame.Parameters), func(_ context.Context, _ ParameterInfo, dst []sqltype.Value, _ sqltype.InputEnvironment) error {
		copy(dst, frame.Parameters)
		return nil
	})
	if err != nil {
		return value, err
	}
	if result.Rows == nil {
		return value, fmt.Errorf("SQL routine SELECT has no row relation")
	}
	defer func() {
		closeErr := result.Rows.Close()
		if err == nil {
			err = closeErr
		}
	}()
	row := []sqltype.Value{{}}
	present, err := result.Rows.Next(parent.op.Context(), row)
	if err != nil {
		return value, err
	}
	if !present {
		value = sqltype.Null(resultUsage.Type)
	} else {
		value, err = child.sqlRoutineResultPlan.Apply(parent.op.Context(), row[0], child.input, child.output)
		if err != nil {
			return value, err
		}
	}
	e.mu.Lock()
	if parent.op.session.sqlRoutines == nil {
		parent.op.session.sqlRoutines = make(map[ids.ObjectAddress]sqlRoutineAnalysis)
	}
	parent.op.session.sqlRoutines[d.Address()] = sqlRoutineAnalysis{d.SQL, *child.Expected}
	e.mu.Unlock()
	return value, nil
}

// Query demand is selected before opening producers. Sort/aggregate semantics
// remain query-owned; only unnecessary later projected output is not requested.
func finalizeSQLRoutineQuery(ctx context.Context, root LogicalNode, source *querySource, parameters []sqltype.Value) (rows RowResult, count int, err error) {
	defer source.close()
	defer func() {
		finishErr := finishForeignSources(ctx, source)
		if err == nil {
			err = finishErr
		}
	}()
	project, ok := root.(*projectNode)
	if !ok || len(project.Schema()) != 1 {
		return nil, 0, fmt.Errorf("invalid SQL routine projection")
	}
	state := queryState{source: source, parameters: parameters, locking: source.locks != nil, hashFilter: hashQueryFilter(root)}
	defer state.releaseMemberships()
	if scope := source.liveScope(); scope != nil {
		state.input, state.output = scope.input, scope.output
	}
	stage, err := openQueryProjectStage(ctx, &state, project)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		closeErr := stage.Close()
		if err == nil {
			err = closeErr
		}
	}()
	row := make([]sqltype.Value, 1)
	present, err := stage.nextProjected(ctx, row)
	if err != nil {
		return nil, 0, err
	}
	if present {
		count = 1
	}
	result := newFinalRows(project.Schema(), count)
	result.output = state.output
	if present {
		result.values = row
	}
	return result, count, nil
}

var _ expr.Composite = sqlRoutineExpression{}

func sameMappedSQLRoutine(current, captured *catalog.Procedure, typ func(*sqltype.Descriptor) *sqltype.Descriptor) bool {
	if current.SQL == nil || captured.SQL == nil || current.SQL.Source != captured.SQL.Source || len(current.SQL.Inputs) != len(captured.SQL.Inputs) {
		return false
	}
	result := typ(captured.SQL.Result.Type)
	if result == nil || result.ID() != current.SQL.Result.Type.ID() {
		return false
	}
	for i, input := range captured.SQL.Inputs {
		mapped := typ(input.Type)
		if mapped == nil || mapped.ID() != current.SQL.Inputs[i].Type.ID() {
			return false
		}
	}
	return true
}
