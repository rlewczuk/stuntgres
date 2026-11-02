package exec

import (
	"errors"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// AdmissionAuthority is the borrowed local DDL admission authority of
// EvalAdmission. The runtime copies the slot with the context (the borrowed
// pointer, never the pointee) and reads it only through AdmissionAccess. A nil
// pointer is stored as an untyped nil.
type AdmissionAuthority interface{ AdmissionAccess() RoutineAccess }

type RoutineBodyContext struct {
	Outer     *catalog.Procedure
	Statement bool
}

func (context RoutineBodyContext) Annotate(err error) error {
	if err == nil || context.Outer == nil {
		return err
	}
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) && diagnostic.Code == "42501" {
		copy := *diagnostic
		phase := "during inlining"
		if context.Statement {
			phase = "statement 1"
		}
		copy.Context = fmt.Sprintf(`SQL function "%s" %s`, context.Outer.Name(), phase)
		return sqlstate.WithDiagnostic(err, &copy)
	}
	return err
}

func (c *Context) EvalPLAssignment(n plan.PLAssignmentExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	previous := c.PLAssignmentPlanning
	c.PLAssignmentPlanning = true
	defer func() { c.PLAssignmentPlanning = previous }()
	return n.Child.Eval(c)
}

func (c *Context) EvalSQLRoutine(n plan.SQLRoutineExpr) (sqltype.Value, error) {
	if n.Procedure.RoutineDefinition().ReturnsSet {
		return sqltype.Value{}, fmt.Errorf("set-returning SQL routine requires source evaluation")
	}
	return c.evalSQLRoutine(n, nil)
}

func (c *Context) OpenSQLRoutine(n plan.SQLRoutineExpr) (expr.SourceCursor, error) {
	var values []sqltype.Value
	if _, err := c.evalSQLRoutine(n, &values); err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(n.Usage.Type, values), nil
}

func (c *Context) evalSQLRoutine(n plan.SQLRoutineExpr, values *[]sqltype.Value) (value sqltype.Value, err error) {
	if err = EvaluationContext(c); err != nil {
		return value, err
	}
	var accepted *AcceptedDefaultInvocation
	if call := c.defaultCall; call != nil {
		if !call.matchesPrepared(n) {
			return value, ids.ErrCatalogReplaced
		}
		accepted, err = c.acceptedDefaultInvocation(call)
		if err != nil {
			return value, err
		}
	}
	// Argument expressions do not borrow the parent's exact selected call.
	// Written nested wrappers establish their own occurrence; hidden defaults
	// additionally enter their independent declaration-owned manifest below.
	selected := c.defaultCall
	c.defaultCall = nil
	defer func() { c.defaultCall = selected }()
	frame := proc.NewFrame(n.Signature.Inputs)
	defer frame.Close()
	var tail []sqltype.Value
	var expandedSlot = -1
	for slot, source := range n.InputMap {
		if source.Expanded {
			expandedSlot = slot
			tail = make([]sqltype.Value, source.Count)
			break
		}
	}
	for child, arg := range n.Args {
		slot := n.ChildSlots[child]
		var v sqltype.Value
		var failure error
		if child >= n.WrittenCount {
			v, failure = c.evalRoutineDefault(n.Ref, slot, n.Procedure.SQLDefaults.Dependencies(), arg, n.Info)
		} else {
			v, failure = arg.Eval(c)
		}
		if failure != nil {
			return value, failure
		}
		if slot == expandedSlot {
			tail[child-n.InputMap[slot].Start] = v
		} else if err = frame.Bind(slot, v, arg.ResultUsage()); err != nil {
			return value, err
		}
	}
	if expandedSlot >= 0 {
		usage := n.Signature.Inputs[expandedSlot]
		packed, failure := sqltype.OwnedArrayValue(usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(tail))}}, tail)
		if failure != nil {
			return value, failure
		}
		if err = frame.Bind(expandedSlot, packed, usage); err != nil {
			return value, err
		}
	}
	host := c.sqlRoutineInvocationHost()
	if host == nil {
		return value, fmt.Errorf("SQL routine invocation has no statement owner")
	}
	if values != nil {
		*values, err = host.InvokeSQLRoutineSet(n.Procedure, frame, n.Usage, n.InputCollation, c.PLAssignmentPlanning && !c.DomainValueActive, c.ConstantPlanning)
		return value, err
	}
	value, err = host.InvokeSQLRoutine(n.Procedure, frame, n.Usage, n.InputCollation, c.PLAssignmentPlanning && !c.DomainValueActive, c.ConstantPlanning, accepted)
	if err != nil {
		return value, err // The body owner annotates its actual command ordinal.
	}
	if n.Procedure.RoutineKind == catalog.ProcedureRoutine && n.Procedure.RoutineDefinition().Void() {
		value = sqltype.VoidValue(n.Usage.Type)
	}
	return frame.Result(value, n.Usage)
}

// The explicit statement host wins over a query source's host for routine
// invocation. Accepted DEFAULT activation must authenticate this same owner.
func (c *Context) sqlRoutineInvocationHost() Host {
	if c.Host != nil {
		return c.Host
	}
	if c.Query != nil && c.Query.Source != nil {
		return c.Query.Source.LiveHost()
	}
	return nil
}

// The declaration recipe, not the caller/table manifest, owns hidden defaults.
func (c *Context) evalRoutineDefault(owner ids.ObjectRef, slot int, dependencies []catalog.ExpressionDependency, node expr.Expr, site expr.Info) (sqltype.Value, error) {
	previousOrigin, recipe, graft := c.FoldOrigin, c.RoutineRecipe, c.RoutineGraft
	checks, refs, consumer := c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer
	accepted, activation, call := c.AcceptedDefault, c.defaultActivation, c.defaultCall
	c.SelectRoutineDefault(owner, slot, dependencies, site)
	defer func() {
		c.FoldOrigin, c.RoutineRecipe, c.RoutineGraft = previousOrigin, recipe, graft
		c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer = checks, refs, consumer
		c.AcceptedDefault, c.defaultActivation, c.defaultCall = accepted, activation, call
	}()
	return node.Eval(c)
}

// SelectRoutineDefault selects the declaration's original hidden-argument
// recipe. Written arguments stay with their caller; this boundary is shared by
// planning traversal, scalar admission and actual default evaluation.
func (c *Context) SelectRoutineDefault(owner ids.ObjectRef, slot int, dependencies []catalog.ExpressionDependency, site expr.Info) {
	c.AcceptedDefault, c.defaultActivation, c.defaultCall = nil, nil, nil
	c.FoldOrigin = FoldOrigin{Owner: owner, Attribute: int32(slot + 1), Expansion: site.Source, Site: site.Occurrence}
	if c.Catalog.Owner() != nil {
		if d := c.Catalog.Get(owner.Address); d != nil {
			c.FoldOrigin.Revision = d.Revision()
		}
	}
	c.RoutineRecipe, c.RoutineGraft = true, false
	c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer = dependencies, nil, nil
}

// EarthCoordinatePreserveParameterDemand borrows the existing command-invocation
// capture, never the session store or a retained prepared statement's last mode.
func (c *Context) EarthCoordinatePreserveParameterDemand() bool {
	return c != nil && c.PreserveParameterDemand
}

// EarthCoordinateDiagnosticNames follows checked immutable query/helper roles
// across presentation changes. It is demanded only on the body-error path.
func (c *Context) EarthCoordinateDiagnosticNames(n functions.EarthCoordinate) (string, string, error) {
	if err := EvaluationContext(c); err != nil {
		return "", "", err
	}
	captured, err := ExpressionRoutineDescriptor(n)
	if err != nil {
		return "", "", err
	}
	if captured == nil {
		return "", "", fmt.Errorf("stuntgres: missing earth coordinate routine identity")
	}
	view := preparation.DomainCatalog(c)
	current, err := ValidateStoredRoutineDescriptor(view, captured)
	if err != nil {
		return "", "", err
	}
	if n.Plan.Operation() != sqltype.EarthBox && n.Plan.Operation() != sqltype.EarthDistance {
		return current.Name(), "", nil
	}
	helper, err := extension.EarthQueryConversionHelper(view, current)
	if err != nil {
		return "", "", err
	}
	return current.Name(), helper.Name(), nil
}

// CitextRegexpDiagnosticName borrows the current admitted declaration rather
// than the immutable kernel symbol or a name retained before ALTER RENAME.
func (c *Context) CitextRegexpDiagnosticName(n functions.RegexpCall) (string, error) {
	if err := EvaluationContext(c); err != nil {
		return "", err
	}
	captured, err := ExpressionRoutineDescriptor(n)
	if err != nil {
		return "", err
	}
	if captured == nil {
		return "", fmt.Errorf("stuntgres: missing citext regexp routine identity")
	}
	current, err := ValidateStoredRoutineDescriptor(preparation.DomainCatalog(c), captured)
	if err != nil {
		return "", err
	}
	return current.Name(), nil
}

func SQLRoutineContext(err error, name string, runtime bool) error {
	statement := 0
	if runtime {
		statement = 1
	}
	return SQLRoutineStatementContext(err, name, statement)
}

// SQLRoutineStatementContext preserves leaf diagnostics and nesting order.
// Zero identifies producer-free definition validation.
func SQLRoutineStatementContext(err error, name string, statement int) error {
	if err == nil {
		return nil
	}
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		return err
	}
	copy := *diagnostic
	context := SQLRoutineStatementFrame(name, statement)
	if copy.Context != "" {
		copy.Context += "\n"
	}
	copy.Context += context
	return sqlstate.WithDiagnostic(err, &copy)
}

// SQLRoutineStatementFrame is shared by error unwinding and buffered notices.
func SQLRoutineStatementFrame(name string, statement int) string {
	context := "SQL function " + strconv.Quote(name)
	if statement != 0 {
		context += " statement " + strconv.Itoa(statement)
	}
	return context
}
