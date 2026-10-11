package engine

import (
	"context"
	"fmt"
	"math"
	"time"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type datetimeSleepKind uint8

const (
	datetimeSleepSeconds datetimeSleepKind = iota + 1
	datetimeSleepInterval
	datetimeSleepUntil
)

func datetimeSleepDefinition(name string) (datetimeSleepKind, sqltype.ID) {
	switch name {
	case "pg_sleep":
		return datetimeSleepSeconds, sqltype.Float8
	case "pg_sleep_for":
		return datetimeSleepInterval, sqltype.Interval
	case "pg_sleep_until":
		return datetimeSleepUntil, sqltype.Timestamptz
	}
	return 0, 0
}
func datetimeSleepName(kind datetimeSleepKind) string {
	switch kind {
	case datetimeSleepSeconds:
		return "pg_sleep"
	case datetimeSleepInterval:
		return "pg_sleep_for"
	case datetimeSleepUntil:
		return "pg_sleep_until"
	}
	return ""
}

type datetimeSleepExpression struct {
	expressionInfo
	kind       datetimeSleepKind
	arg        BoundExpr
	clockUsage sqltype.Usage
	calendar   sqltype.Arithmetic
}

func (x datetimeSleepExpression) validStored() bool {
	_, id := datetimeSleepDefinition(datetimeSleepName(x.kind))
	if id == 0 || x.arg == nil {
		return false
	}
	argUsage := x.arg.ResultUsage()
	return x.usage.Type != nil && x.usage.Valid() && x.usage.Type.ID() == sqltype.Void &&
		x.properties.Volatility == Volatile && argUsage.Type != nil && argUsage.Valid() && argUsage.Type.ID() == id &&
		x.clockUsage.Type != nil && x.clockUsage.Valid() && x.clockUsage.Type.ID() == sqltype.Timestamptz &&
		(x.kind != datetimeSleepInterval || x.calendar.ResultType() == x.clockUsage.Type)
}
func (x datetimeSleepExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if !x.validStored() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid datetime sleep expression")
	}
	v, err := x.arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if v.IsNull() {
		return sqltype.Null(x.usage.Type), nil
	}
	switch x.kind {
	case datetimeSleepSeconds:
		var seconds float64
		seconds, err = v.Float()
		if err == nil {
			err = datetimeSleepWait(c.Context, seconds)
		}
	case datetimeSleepInterval:
		var start sqltype.Value
		start, err = datetimeSleepClock(c, x.clockUsage)
		if err == nil {
			var target sqltype.Value
			target, err = x.calendar.Binary(c.Context, start, v, c.Input)
			if err == nil {
				var now sqltype.Value
				now, err = datetimeSleepClock(c, x.clockUsage)
				if err == nil {
					targetStamp, _ := target.Timestamp()
					nowStamp, _ := now.Timestamp()
					err = datetimeSleepWait(c.Context, datetimeSleepDifference(targetStamp, nowStamp))
				}
			}
		}
	case datetimeSleepUntil:
		target, _ := v.Timestamp()
		err = datetimeSleepWaitRemaining(c.Context, func() (float64, error) {
			now, e := datetimeSleepClock(c, x.clockUsage)
			if e != nil {
				return 0, e
			}
			stamp, _ := now.Timestamp()
			return datetimeSleepDifference(target, stamp), nil
		})
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.VoidValue(x.usage.Type), nil
}
func datetimeSleepClock(c *EvalContext, usage sqltype.Usage) (sqltype.Value, error) {
	input := c.Input
	input.TransactionStart = time.Now().UTC().Truncate(time.Microsecond)
	return sqltype.CurrentDateTime(usage, input)
}

// Divide before subtracting so even opposite finite SQL timestamp extrema cannot
// overflow. Current near-deadline differences retain microsecond precision.
func datetimeSleepDifference(target, now int64) float64 {
	if target == sqltype.TimestampPositiveInfinity {
		return math.Inf(1)
	}
	if target == sqltype.TimestampNegativeInfinity {
		return math.Inf(-1)
	}
	return float64(target/1000000-now/1000000) + float64(target%1000000-now%1000000)/1000000
}
func datetimeSleepChunk(seconds float64) time.Duration {
	if seconds >= 600 {
		return 600 * time.Second
	}
	return time.Duration(math.Ceil(seconds * float64(time.Second)))
}
func datetimeSleepWait(ctx context.Context, seconds float64) error {
	start := time.Now()
	return datetimeSleepWaitRemaining(ctx, func() (float64, error) { return seconds - time.Since(start).Seconds(), nil })
}
func datetimeSleepWaitRemaining(ctx context.Context, remaining func() (float64, error)) error {
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		seconds, err := remaining()
		if err != nil {
			return err
		}
		if !(seconds > 0) {
			return nil
		}
		chunk := datetimeSleepChunk(seconds)
		if timer == nil {
			timer = time.NewTimer(chunk)
		} else {
			timer.Reset(chunk)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (b *expressionBinder) bindDatetimeSleep(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [1]comparisonOperand
	operands := small[:min(len(raw.Args), 1)]
	if len(raw.Args) > 1 && len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
	}
	for i, child := range raw.Args {
		operand, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = operand
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	signature := func() error {
		names := make([]string, len(operands))
		for i, operand := range operands {
			names[i] = functionNamedType(raw, i, datetimeFormattingTypeName(operand))
		}
		return functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind, id := datetimeSleepDefinition(name)
	if kind == 0 || !visible || raw.Variadic || len(raw.ArgNames) != 0 || len(raw.Args) != 1 {
		return nil, signature()
	}
	usage := b.typ(id).Usage()
	if descriptor := operands[0].descriptor(); descriptor != nil {
		if _, err := sqltype.PlanCoercion(operands[0].node.ResultUsage(), usage, sqltype.Implicit); err != nil {
			return nil, signature()
		}
	}
	switch {
	case raw.Star:
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	arg, err := b.finalizeOperand(operands[0], usage.Type)
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != usage {
		plan, err := sqltype.PlanCoercion(arg.ResultUsage(), usage, sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		arg = coercionExpression{b.info(usage, arg.SourcePosition(), arg.Properties()), arg, plan, sqltype.Implicit}
	}
	properties := mergeExpressionProperties(ExprProperties{Volatility: Volatile}, arg.Properties())
	clockUsage := b.typ(sqltype.Timestamptz).Usage()
	var calendar sqltype.Arithmetic
	if kind == datetimeSleepInterval {
		calendar, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, clockUsage.Type, arg.ResultUsage().Type, clockUsage.Type)
		if err != nil {
			return nil, err
		}
	}
	return datetimeSleepExpression{b.info(b.typ(sqltype.Void).Usage(), raw.Pos, properties), kind, arg, clockUsage, calendar}, nil
}
