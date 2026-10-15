package functions

import (
	"fmt"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The engine supplies its existing coherent evaluation scope, including domain
// checks; the provider never captures an engine or resolves catalog names.
type JSONBRecordContext interface {
	Context
	FunctionJSONBRecordInput() sqltype.InputEnvironment
}

type JSONBRecord[E Expression[C], C JSONBRecordContext, P any] struct {
	Info[P]
	Name        string
	Args        []E
	Definitions bool
	// Unresolved describes static result-layout availability, not whether a
	// non-NULL anonymous base can supply a runtime tuple descriptor.
	Unresolved bool
}

func (e JSONBRecord[E, C, P]) ReturnsSet() bool {
	return e.Name == "jsonb_populate_recordset" || e.Name == "jsonb_to_recordset"
}
func (e JSONBRecord[E, C, P]) RecordUsage() sqltype.Usage {
	if e.Definitions && e.Name != "jsonb_populate_record_valid" || e.Name == "jsonb_to_record" || e.Name == "jsonb_to_recordset" {
		return e.Usage
	}
	return e.Args[0].ResultUsage()
}
func (e JSONBRecord[E, C, P]) MapChildren(child func(E) (E, bool, error)) (JSONBRecord[E, C, P], bool, error) {
	var args []E
	for i, old := range e.Args {
		next, changed, err := child(old)
		if err != nil {
			return e, false, err
		}
		if changed && args == nil {
			args = append([]E(nil), e.Args...)
		}
		if args != nil {
			args[i] = next
		}
	}
	if args == nil {
		return e, false, nil
	}
	e.Args = args
	return e, true, nil
}
func (e JSONBRecord[E, C, P]) Reconstruct(info Info[P], args []E) (JSONBRecord[E, C, P], error) {
	count := 2
	switch e.Name {
	case "jsonb_to_record", "jsonb_to_recordset":
		count = 1
	case "jsonb_populate_record", "jsonb_populate_recordset", "jsonb_populate_record_valid":
	default:
		return JSONBRecord[E, C, P]{}, fmt.Errorf("invalid JSONB record function")
	}
	if len(args) != count || !info.Usage.Valid() || args[count-1].ResultUsage().Type == nil || args[count-1].ResultUsage().Type.ID() != sqltype.JSONB {
		return JSONBRecord[E, C, P]{}, fmt.Errorf("invalid JSONB record arguments")
	}
	if e.Definitions && e.Unresolved {
		return JSONBRecord[E, C, P]{}, fmt.Errorf("conflicting JSONB record shape")
	}
	if count == 1 && !info.Usage.Type.IsComposite() {
		return JSONBRecord[E, C, P]{}, fmt.Errorf("invalid JSONB record result")
	}
	if count == 2 {
		if args[0].ResultUsage().Type == nil {
			return JSONBRecord[E, C, P]{}, fmt.Errorf("missing JSONB record base")
		}
		if e.Name == "jsonb_populate_record_valid" {
			if e.Definitions || info.Usage.Type.ID() != sqltype.Bool {
				return JSONBRecord[E, C, P]{}, fmt.Errorf("invalid JSONB validity result")
			}
		} else if info.Usage != args[0].ResultUsage() && !(e.Definitions && info.Usage.Type.ID() == 2249 && args[0].ResultUsage().Type.ID() == 2249) {
			return JSONBRecord[E, C, P]{}, fmt.Errorf("foreign JSONB record result generation")
		}
	}
	e.Info, e.Args = info, args
	return e, nil
}
func (e JSONBRecord[E, C, P]) inputs(c C) (sqltype.Value, sqltype.Value, sqltype.Usage, error) {
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
func (e JSONBRecord[E, C, P]) Eval(c C) (sqltype.Value, error) {
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
func (e JSONBRecord[E, C, P]) EvalSet(c C) ([]sqltype.Value, error) {
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
