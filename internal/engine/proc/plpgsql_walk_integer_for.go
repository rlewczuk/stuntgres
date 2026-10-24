package proc

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

const PLIntegerForAction = "at FOR with integer loop variable"

// executePLIntegerFor is also the public fixture's live cancellation admission
// frame. Progression is independent of writes to the SQL-visible counter.
func (r *PLRun) executePLIntegerFor(in PLInstruction) (value sqltype.Value, flow PLFlow, err error) {
	loop, raw := in.IntegerFor, in.Syntax.IntegerFor
	defer func() { r.Frame.Values[loop.Counter] = sqltype.Value{} }()
	at := PLOrigin{Position: in.Syntax.Pos, Action: PLIntegerForAction}
	usage := r.Frame.Usage(loop.Counter)
	operand := func(q *parser.Select, span parser.PLExpression, nullMessage string) (int64, error) {
		v, _, err := r.Host.Evaluate(r.Frame, q, at)
		expressionError := err != nil
		if err == nil {
			v, err = r.Host.Convert(v, usage, at)
		}
		if err == nil && v.IsNull() {
			err = plError("22004", nullMessage)
		}
		if err != nil {
			return 0, PLRuntimeError(err, r.Definition, r.Host.Identity, span.Start, span.End, in.Syntax.Pos, PLIntegerForAction, expressionError)
		}
		return v.Integer()
	}
	lower, err := operand(loop.Lower, raw.Lower, "lower bound of FOR loop cannot be null")
	if err != nil {
		return value, flow, err
	}
	upper, err := operand(loop.Upper, raw.Upper, "upper bound of FOR loop cannot be null")
	if err != nil {
		return value, flow, err
	}
	step := int64(1)
	if loop.Step != nil {
		step, err = operand(loop.Step, raw.Step, "BY value of FOR loop cannot be null")
		if err != nil {
			return value, flow, err
		}
		if step <= 0 {
			return value, flow, PLRuntimeError(plError("22023", "BY value of FOR loop must be greater than zero"), r.Definition, r.Host.Identity, raw.Step.Start, raw.Step.End, in.Syntax.Pos, PLIntegerForAction, false)
		}
	}
	iterated := false
	for current := lower; !loop.Reverse && current <= upper || loop.Reverse && current >= upper; {
		if err := r.Host.Context().Err(); err != nil {
			return value, flow, err
		}
		counter, err := sqltype.Integer(usage.Type, current)
		if err != nil {
			return value, flow, err
		}
		r.Frame.Values[loop.Counter] = counter
		iterated = true
		value, flow, err = r.Block(loop.Body)
		if err != nil {
			return value, flow, err
		}
		if flow.Kind != PLNormal && !(flow.Kind == PLContinue && flow.Target == loop.Body.Control) {
			if flow.Kind == PLExit && flow.Target == loop.Body.Control {
				flow = PLFlow{}
			}
			r.Frame.SetFound(iterated)
			return value, flow, nil
		}
		if loop.Reverse {
			current -= step
		} else {
			current += step
		}
		if current < -2147483648 || current > 2147483647 {
			break
		}
	}
	r.Frame.SetFound(iterated)
	return value, PLFlow{}, nil
}
