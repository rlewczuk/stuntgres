package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type JSONBRecord struct {
	expr.Info
	Name        string
	Args        []expr.Expr
	Definitions bool
	// Unresolved describes static result-layout availability, not whether a
	// non-NULL anonymous base can supply a runtime tuple descriptor.
	Unresolved bool
}

func (e JSONBRecord) ReturnsSet() bool {
	return e.Name == "jsonb_populate_recordset" || e.Name == "jsonb_to_recordset"
}
func (e JSONBRecord) RecordUsage() sqltype.Usage {
	if e.Definitions && e.Name != "jsonb_populate_record_valid" || e.Name == "jsonb_to_record" || e.Name == "jsonb_to_recordset" {
		return e.Usage
	}
	return e.Args[0].ResultUsage()
}
func (e JSONBRecord) Reconstruct(info expr.Info, args []expr.Expr) (JSONBRecord, error) {
	count := 2
	switch e.Name {
	case "jsonb_to_record", "jsonb_to_recordset":
		count = 1
	case "jsonb_populate_record", "jsonb_populate_recordset", "jsonb_populate_record_valid":
	default:
		return JSONBRecord{}, fmt.Errorf("invalid JSONB record function")
	}
	if len(args) != count || !info.Usage.Valid() || args[count-1].ResultUsage().Type == nil || args[count-1].ResultUsage().Type.ID() != sqltype.JSONB {
		return JSONBRecord{}, fmt.Errorf("invalid JSONB record arguments")
	}
	if e.Definitions && e.Unresolved {
		return JSONBRecord{}, fmt.Errorf("conflicting JSONB record shape")
	}
	if count == 1 && !info.Usage.Type.IsComposite() {
		return JSONBRecord{}, fmt.Errorf("invalid JSONB record result")
	}
	if count == 2 {
		if args[0].ResultUsage().Type == nil {
			return JSONBRecord{}, fmt.Errorf("missing JSONB record base")
		}
		if e.Name == "jsonb_populate_record_valid" {
			if e.Definitions || info.Usage.Type.ID() != sqltype.Bool {
				return JSONBRecord{}, fmt.Errorf("invalid JSONB validity result")
			}
		} else if info.Usage != args[0].ResultUsage() && !(e.Definitions && info.Usage.Type.ID() == 2249 && args[0].ResultUsage().Type.ID() == 2249) {
			return JSONBRecord{}, fmt.Errorf("foreign JSONB record result generation")
		}
	}
	e.Info, e.Args = info, args
	return e, nil
}

// JSONBRecord implements expr.Composite.
var _ expr.Composite = JSONBRecord{}

func (e JSONBRecord) Children() []expr.Expr { return e.Args }
func (e JSONBRecord) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBRecord", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}
func (e JSONBRecord) inputs(c expr.Context) (sqltype.Value, sqltype.Value, sqltype.Usage, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, sqltype.Value{}, sqltype.Usage{}, err
	}
	var values [2]sqltype.Value
	if len(e.Args) < 1 || len(e.Args) > 2 {
		return sqltype.Value{}, sqltype.Value{}, sqltype.Usage{}, fmt.Errorf("invalid JSONB record arity")
	}
	for i, arg := range e.Args {
		values[i], err = arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, sqltype.Value{}, sqltype.Usage{}, err
		}
	}
	name := e.Name
	if name == "jsonb_populate_record_valid" {
		name = "jsonb_populate_record"
	}
	missing := func() (sqltype.Value, sqltype.Value, sqltype.Usage, error) {
		err := sqlError("0A000", "could not determine row type for result of "+name).(*sqlstate.Error)
		err.Hint = "Provide a non-null record argument, or call the function in the FROM clause using a column definition list."
		return sqltype.Value{}, sqltype.Value{}, sqltype.Usage{}, err
	}
	usage := e.RecordUsage()
	base, document := sqltype.Null(usage.Type), values[len(e.Args)-1]
	if len(e.Args) == 1 {
		if e.Name == "jsonb_to_record" && document.IsNull() {
			return base, document, usage, nil
		}
		if e.Unresolved {
			return missing()
		}
	} else {
		declared := e.Args[0].ResultUsage()
		if !declared.Type.BaseType().IsComposite() && !declared.Type.IsDeferredRecord() {
			return sqltype.Value{}, sqltype.Value{}, sqltype.Usage{}, sqlError("42804", "first argument of "+name+" must be a row type")
		}
		base = values[0]
		if base.Type() != declared.Type {
			return sqltype.Value{}, sqltype.Value{}, sqltype.Usage{}, fmt.Errorf("foreign JSONB record base generation")
		}
		if declared.Type.ID() == 2249 {
			if base.IsNull() {
				if !e.Definitions {
					return missing()
				}
				// Adapt only the evaluated SQL NULL. Never substitute or retag
				// the argument expression or inject a descriptor into an inner call.
				usage = e.Usage
				base = sqltype.Null(usage.Type)
			} else {
				usage = base.Type().Usage()
			}
		} else {
			usage = declared
		}
	}
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, sqltype.Value{}, sqltype.Usage{}, err
	}
	return base, document, usage, nil
}
func (e JSONBRecord) Eval(c expr.Context) (sqltype.Value, error) {
	if e.ReturnsSet() {
		if _, err := evaluationContext(c); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Value{}, fmt.Errorf("stuntgres: unlifted JSONB recordset call")
	}
	base, document, destination, err := e.inputs(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	ctx, input := c.FunctionContext(), c.FunctionJSONBRecordInput()
	value, err := sqltype.JSONBPopulateRecord(ctx, destination, base, document, input)
	if e.Name == "jsonb_populate_record_valid" {
		if err != nil {
			if sqltype.JSONBRecordSoftError(err) {
				return sqltype.Boolean(e.Usage.Type, false), nil
			}
			return sqltype.Value{}, err
		}
		if document.IsNull() && base.IsNull() {
			return sqltype.Null(e.Usage.Type), nil
		}
		return sqltype.Boolean(e.Usage.Type, true), nil
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	if e.Definitions && len(e.Args) == 2 && destination.Type != e.Usage.Type {
		plan, err := sqltype.PlanJSONBRecordResult(destination.Type, e.Usage.Type, input)
		if err != nil {
			return sqltype.Value{}, err
		}
		return plan.Apply(ctx, value, input)
	}
	return value, nil
}
func (e JSONBRecord) EvalSet(c expr.Context) ([]sqltype.Value, error) {
	if !e.ReturnsSet() {
		return nil, fmt.Errorf("invalid JSONB recordset call")
	}
	base, document, destination, err := e.inputs(c)
	if err != nil {
		return nil, err
	}
	ctx, input := c.FunctionContext(), c.FunctionJSONBRecordInput()
	rows, err := sqltype.JSONBPopulateRecordset(ctx, e.Name, destination, base, document, input)
	if err != nil {
		return nil, err
	}
	if e.Definitions && len(e.Args) == 2 && !document.IsNull() && destination.Type != e.Usage.Type {
		plan, err := sqltype.PlanJSONBRecordResult(destination.Type, e.Usage.Type, input)
		if err != nil {
			return nil, err
		}
		for i, row := range rows {
			rows[i], err = plan.Apply(ctx, row, input)
			if err != nil {
				return nil, err
			}
		}
	}
	return rows, ctx.Err()
}

func (e JSONBRecord) IntrinsicVolatility() expr.Volatility { return expr.Stable }

func (e JSONBRecord) PlanningFold() expr.Fold { return expr.FoldNever }

var _ expr.Renderable = JSONBRecord{}

// Render implements expr.Renderable.
func (e JSONBRecord) Render() (expr.Rendering, error) {
	spec := callRendering("JSONBRecord "+e.Name, e.Args, e.Name, e.Args)
	spec.Quote = expr.QuoteWhenNeeded
	spec.Replay.Validate = unsupportedReplay
	return spec, nil
}

// SameGroup implements expr.GroupIdentity.
func (e JSONBRecord) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(JSONBRecord)
	if !ok || e.Name != o.Name || e.Unresolved != o.Unresolved || e.Definitions != o.Definitions || !sqltype.SameRecordUsage(e.ResultUsage(), o.ResultUsage()) || len(e.Args) != len(o.Args) {
		return false
	}
	for i, arg := range e.Args {
		if !same(arg, o.Args[i]) {
			return false
		}
	}
	return true
}

// PreflightFold implements expr.Preflighting.
func (e JSONBRecord) PreflightFold() expr.Preflight { return expr.PreflightChildren }

// Substitute implements expr.Substitutable.
func (e JSONBRecord) Substitute(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("JSONBRecord", children, len(e.Args)); err != nil {
		return nil, err
	}
	e.Info, e.Args = info, children
	return e, nil
}

func (e JSONBRecord) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	args := e.Children()
	return expr.CaptureOperands{Args: args, Count: len(args)}, true, nil
}
