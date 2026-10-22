package functions

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// SQLJSONQuery owns runtime child ordering and lazy behavior selection. The
// sqltype plan owns selection/cardinality and the recoverable result boundary.
type SQLJSONQuery struct {
	expr.Info
	Raw      parser.SQLJSONQueryExpr
	Args     []expr.Expr
	Plan     sqltype.SQLJSONQueryPlan
	Defaults [2]sqltype.CoercionPlan
	Column   string
}

func NewSQLJSONQuery(info expr.Info, raw parser.SQLJSONQueryExpr, args []expr.Expr, jsonb sqltype.Usage) (SQLJSONQuery, error) {
	count := 2 + len(raw.Passing)
	if len(args) < count {
		return SQLJSONQuery{}, fmt.Errorf("invalid SQL/JSON query children")
	}
	usages := make([]sqltype.Usage, len(raw.Passing))
	names := make([]string, len(raw.Passing))
	for i, binding := range raw.Passing {
		usages[i] = args[i+2].ResultUsage()
		names[i] = binding.Name.Text
	}
	bind := sqltype.BindSQLJSONQuery
	if raw.TableColumn != "" {
		bind = sqltype.BindSQLJSONTableColumn
	}
	plan, err := bind(raw.Kind, raw.Wrapper, raw.Quotes == "omit", info.Usage, args[0].ResultUsage(), args[1].ResultUsage(), jsonb, usages, names)
	if err != nil {
		return SQLJSONQuery{}, err
	}
	if raw.TableColumn != "" {
		plan = plan.TableColumn(raw.TableColumn, info.Usage)
	}
	node := SQLJSONQuery{Info: info, Raw: raw, Args: args, Plan: plan, Column: raw.TableColumn}
	for i, behavior := range []parser.SQLJSONBehavior{raw.Empty, raw.Error} {
		if behavior.Default != nil {
			if count >= len(args) {
				return SQLJSONQuery{}, fmt.Errorf("missing SQL/JSON default")
			}
			node.Defaults[i], err = sqltype.PlanCoercion(args[count].ResultUsage(), info.Usage, sqltype.Assignment)
			if err != nil {
				return SQLJSONQuery{}, err
			}
			count++
		}
	}
	if count != len(args) {
		return SQLJSONQuery{}, fmt.Errorf("invalid SQL/JSON default children")
	}
	return node, nil
}

func (e SQLJSONQuery) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	document, err := e.Args[0].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.evalDocument(ctx, c, document)
}

func (e SQLJSONQuery) evalDocument(ctx context.Context, c expr.Context, document sqltype.Value) (sqltype.Value, error) {
	if document.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	path, err := e.Args[1].Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if path.IsNull() {
		return sqltype.Null(e.Usage.Type), nil
	}
	var local [8]sqltype.Value
	values := local[:min(len(e.Raw.Passing), len(local))]
	if len(e.Raw.Passing) > len(local) {
		values = make([]sqltype.Value, len(e.Raw.Passing))
	}
	for i := range values {
		values[i], err = e.Args[i+2].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
	}
	outcome, err := e.Plan.Evaluate(ctx, document, path, values, c.FunctionInput(), c.FunctionOutput())
	if err != nil {
		return sqltype.Value{}, err
	}
	if !outcome.Empty && outcome.Error == nil {
		return outcome.Value, nil
	}
	behavior, branch, index := e.Raw.Error, "ERROR", 1
	if outcome.Empty {
		behavior, branch, index = e.Raw.Empty, "EMPTY", 0
	}
	kind := behavior.Kind
	if kind == "" {
		kind = "null"
		if e.Raw.Kind == "json_exists" {
			kind = "false"
		}
	}
	if kind == "error" {
		if outcome.Error != nil {
			return sqltype.Value{}, outcome.Error
		}
		message := "no SQL/JSON item found for specified path"
		if e.Column != "" {
			message += " of column \"" + e.Column + "\""
		}
		return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22035", Message: message}
	}
	if behavior.Default != nil {
		child := 2 + len(e.Raw.Passing)
		if index == 1 && e.Raw.Empty.Default != nil {
			child++
		}
		value, err := e.Args[child].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		value, err = e.Defaults[index].Apply(ctx, value, c.FunctionInput(), c.FunctionOutput())
		if err != nil {
			return sqltype.Value{}, sqltype.SQLJSONBehaviorError(branch, "DEFAULT", err)
		}
		return value, nil
	}
	return e.Plan.Behavior(ctx, kind, branch, c.FunctionInput(), c.FunctionOutput())
}

// ResultNullability publishes the query expression's nullable output even for
// composite destinations, whose descriptor does not supply scalar defaults.
func (e SQLJSONQuery) ResultNullability(expr.ResultSite) (bool, bool) { return true, true }

// sqlJSONDocument owns the execution-time input phase of an unknown document.
// Its child still participates in binding, capture and constant preflight.
type sqlJSONDocument struct {
	expr.Info
	Child expr.Expr
	Plan  sqltype.CoercionPlan
	Table bool
}

func newSQLJSONDocument(info expr.Info, child expr.Expr) (sqlJSONDocument, error) {
	plan, err := sqltype.PlanCoercion(child.ResultUsage(), info.Usage, sqltype.Explicit)
	return sqlJSONDocument{Info: info, Child: child, Plan: plan}, err
}
func (e sqlJSONDocument) Eval(c expr.Context) (sqltype.Value, error) {
	value, err := e.Child.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return e.Plan.Apply(ctx, value, c.FunctionInput(), c.FunctionOutput())
}
func (e sqlJSONDocument) Children() []expr.Expr { return []expr.Expr{e.Child} }
func (e sqlJSONDocument) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 1 {
		return nil, fmt.Errorf("invalid SQL/JSON document children")
	}
	next, err := newSQLJSONDocument(info, children[0])
	next.Table = e.Table
	return next, err
}
func (e sqlJSONDocument) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e sqlJSONDocument) PlanningFold() expr.Fold              { return expr.FoldNever }
func (e sqlJSONDocument) IntrinsicVolatility() expr.Volatility { return expr.Immutable }
func (e sqlJSONDocument) ValidateStoredProvider() error        { return nil }
func (e sqlJSONDocument) ClosureTransparent()                  {}
func (e sqlJSONDocument) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(sqlJSONDocument)
	return ok && e.Usage == o.Usage && same(e.Child, o.Child)
}
func (e sqlJSONDocument) PreflightFold() expr.Preflight {
	if e.Table {
		return expr.PreflightEvaluate
	}
	return expr.PreflightChildren
}
func (e sqlJSONDocument) Render() (expr.Rendering, error) {
	return expr.Rendering{Inputs: []expr.Expr{e.Child}, Form: expr.FormTemplate, Args: []expr.RenderArg{{Expr: e.Child, Listed: true}}, Pieces: []expr.Piece{expr.ArgPiece(0)}}, nil
}

func (e SQLJSONQuery) Children() []expr.Expr { return e.Args }
func (e SQLJSONQuery) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != len(e.Args) {
		return nil, fmt.Errorf("invalid SQL/JSON query children")
	}
	// The JSONB source owner is remapped with the document child.
	node, err := NewSQLJSONQuery(info, e.Raw, children, children[0].ResultUsage())
	if err == nil && e.Column != "" {
		node.Column = e.Column
		node.Plan = node.Plan.TableColumn(e.Column, info.Usage)
	}
	return node, err
}
func (e SQLJSONQuery) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	return e.Rebuild(info, children)
}
func (e SQLJSONQuery) IntrinsicVolatility() expr.Volatility { return e.Property.Volatility }
func (e SQLJSONQuery) ValidateStoredProvider() error        { return nil }
func (e SQLJSONQuery) PreflightFold() expr.Preflight        { return expr.PreflightChildren }
func (e SQLJSONQuery) LooksLikeFunction() bool              { return true }
func (e SQLJSONQuery) ClosureTransparent()                  {}
func (e SQLJSONQuery) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureSQLJSON {
		return expr.CaptureOperands{}, false, nil
	}
	return expr.CaptureOperands{Count: len(e.Args), Indexed: true, JSONSources: true}, true, nil
}
func (e SQLJSONQuery) CaptureOperand(written int) (expr.Expr, error) {
	if written < 0 || written >= len(e.Args) {
		return nil, fmt.Errorf("invalid SQL/JSON query capture operand")
	}
	arg := e.Args[written]
	if document, ok := arg.(sqlJSONDocument); written == 0 && ok {
		return document.Child, nil
	}
	return arg, nil
}
func (e SQLJSONQuery) Render() (expr.Rendering, error) {
	args := make([]expr.RenderArg, len(e.Args))
	for i, arg := range e.Args {
		args[i] = expr.RenderArg{Expr: arg, HideImplicit: true, Listed: true}
	}
	return expr.Rendering{Tree: "SQLJSONQuery " + e.Raw.Kind, Inputs: e.Args, Form: expr.FormTemplate, Args: args, Pieces: SQLJSONQueryPieces(e.Raw), JSONConstructor: true, OwnedReturning: e.Usage}, nil
}
func (e SQLJSONQuery) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(SQLJSONQuery)
	if ok && e.Column != o.Column {
		return false
	}
	if !ok || e.Raw.Kind != o.Raw.Kind || e.Raw.Wrapper != o.Raw.Wrapper || e.Raw.Quotes != o.Raw.Quotes || e.Raw.Empty.Kind != o.Raw.Empty.Kind || e.Raw.Error.Kind != o.Raw.Error.Kind || len(e.Args) != len(o.Args) || len(e.Raw.Passing) != len(o.Raw.Passing) {
		return false
	}
	for i, binding := range e.Raw.Passing {
		if binding.Name.Text != o.Raw.Passing[i].Name.Text {
			return false
		}
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return e.Usage == o.Usage
}
