package functions

import (
	"fmt"
	"math"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"time"
)

type DatetimeSleepKind uint8

const (
	DatetimeSleepSeconds DatetimeSleepKind = iota + 1
	DatetimeSleepInterval
	DatetimeSleepUntil
)

func DatetimeSleepDefinition(name string) (DatetimeSleepKind, sqltype.ID) {
	switch name {
	case "pg_sleep":
		return DatetimeSleepSeconds, sqltype.Float8
	case "pg_sleep_for":
		return DatetimeSleepInterval, sqltype.Interval
	case "pg_sleep_until":
		return DatetimeSleepUntil, sqltype.Timestamptz
	}
	return 0, 0
}
func (kind DatetimeSleepKind) Name() string {
	switch kind {
	case DatetimeSleepSeconds:
		return "pg_sleep"
	case DatetimeSleepInterval:
		return "pg_sleep_for"
	case DatetimeSleepUntil:
		return "pg_sleep_until"
	}
	return ""
}

type datetimeSleepClockContext interface {
	Context
	FunctionSleepClock(sqltype.Usage) (sqltype.Value, error)
}
type DatetimeSleepContext[E, P any] interface {
	datetimeSleepClockContext
	FunctionInput() sqltype.InputEnvironment
	FunctionPropertiesMatch(P, IntrinsicPropertyRule) bool
	FunctionSleepArgumentPresent(E) bool
}
type DatetimeSleep[E Expression[C], C DatetimeSleepContext[E, P], P any] struct {
	Info[P]
	Kind       DatetimeSleepKind
	Arg        E
	ClockUsage sqltype.Usage
	Calendar   sqltype.Arithmetic
}

func (x DatetimeSleep[E, C, P]) Validate(intrinsicMatch, argPresent bool) bool {
	_, id := DatetimeSleepDefinition(x.Kind.Name())
	if id == 0 || !argPresent {
		return false
	}
	argUsage := x.Arg.ResultUsage()
	return x.Usage.Type != nil && x.Usage.Valid() && x.Usage.Type.ID() == sqltype.Void && intrinsicMatch && argUsage.Type != nil && argUsage.Valid() && argUsage.Type.ID() == id && x.ClockUsage.Type != nil && x.ClockUsage.Valid() && x.ClockUsage.Type.ID() == sqltype.Timestamptz && (x.Kind != DatetimeSleepInterval || x.Calendar.ResultType() == x.ClockUsage.Type)
}
func (x DatetimeSleep[E, C, P]) Eval(c C) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !x.Validate(c.FunctionPropertiesMatch(x.Property, VolatileOnly), c.FunctionSleepArgumentPresent(x.Arg)) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid datetime sleep expression")
	}
	value, err := x.Arg.Eval(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.IsNull() {
		return sqltype.Null(x.Usage.Type), nil
	}
	switch x.Kind {
	case DatetimeSleepSeconds:
		var seconds float64
		seconds, err = value.Float()
		if err == nil {
			err = datetimeSleepWait(c, seconds)
		}
	case DatetimeSleepInterval:
		var start sqltype.Value
		start, err = c.FunctionSleepClock(x.ClockUsage)
		if err == nil {
			var target sqltype.Value
			target, err = x.Calendar.Binary(ctx, start, value, c.FunctionInput())
			if err == nil {
				var now sqltype.Value
				now, err = c.FunctionSleepClock(x.ClockUsage)
				if err == nil {
					targetStamp, _ := target.Timestamp()
					nowStamp, _ := now.Timestamp()
					err = datetimeSleepWait(c, datetimeSleepDifference(targetStamp, nowStamp))
				}
			}
		}
	case DatetimeSleepUntil:
		target, _ := value.Timestamp()
		err = datetimeSleepWaitRemaining(c, datetimeSleepRemaining{until: true, target: target, clockUsage: x.ClockUsage})
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.VoidValue(x.Usage.Type), nil
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

// Remaining state borrows the caller at each recheck; it never stores/copies C or
// a context and never allocates a callback for an elapsed or wall-clock deadline.
type datetimeSleepRemaining struct {
	start      time.Time
	seconds    float64
	target     int64
	clockUsage sqltype.Usage
	until      bool
}

func datetimeSleepRemainingSeconds[C datetimeSleepClockContext](c C, remaining datetimeSleepRemaining) (float64, error) {
	if !remaining.until {
		return remaining.seconds - time.Since(remaining.start).Seconds(), nil
	}
	now, err := c.FunctionSleepClock(remaining.clockUsage)
	if err != nil {
		return 0, err
	}
	stamp, _ := now.Timestamp()
	return datetimeSleepDifference(remaining.target, stamp), nil
}
func datetimeSleepWait[C datetimeSleepClockContext](c C, seconds float64) error {
	return datetimeSleepWaitRemaining(c, datetimeSleepRemaining{start: time.Now(), seconds: seconds})
}
func datetimeSleepStopTimer(timer **time.Timer) {
	if *timer != nil {
		(*timer).Stop()
	}
}
func datetimeSleepWaitRemaining[C datetimeSleepClockContext](c C, remaining datetimeSleepRemaining) error {
	ctx := c.FunctionContext()
	var timer *time.Timer
	defer datetimeSleepStopTimer(&timer)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		seconds, err := datetimeSleepRemainingSeconds(c, remaining)
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
func AnalyzeDatetimeSleepArgument[E any, B ArgumentAnalyzer[E]](b B, raw parser.Expr) (Argument[E], error) {
	return b.AnalyzeArgument(raw, AnalysisRequest{Intent: ComparisonArgument})
}
func DatetimeSleepShape(raw parser.FunctionExpr, visible bool, kind DatetimeSleepKind) bool {
	return kind != 0 && visible && !raw.Variadic && len(raw.ArgNames) == 0 && len(raw.Args) == 1
}
func DatetimeSleepCompatible(source, target sqltype.Usage) bool {
	if source.Type == nil {
		return true
	}
	_, err := sqltype.PlanCoercion(source, target, sqltype.Implicit)
	return err == nil
}
func DatetimeSleepDecoration(raw parser.FunctionExpr) string {
	switch {
	case raw.Star:
		return "(*)"
	case raw.Distinct:
		return "DISTINCT"
	case len(raw.Order) != 0:
		return "ORDER BY"
	case raw.Filter != nil:
		return "FILTER"
	}
	return ""
}
func CoerceDatetimeSleepArgument[E any, B ArgumentCoercer[E]](b B, arg Argument[E], target sqltype.Usage) (E, error) {
	return b.CoerceArgument(arg, CoercionRequest{Intent: ExactFormalArgument, Target: target, PreserveProperties: true})
}
func NewDatetimeSleep[E Expression[C], C DatetimeSleepContext[E, P], P any](info Info[P], kind DatetimeSleepKind, arg E, clock sqltype.Usage) (DatetimeSleep[E, C, P], error) {
	var calendar sqltype.Arithmetic
	var err error
	if kind == DatetimeSleepInterval {
		calendar, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, clock.Type, arg.ResultUsage().Type, clock.Type)
	}
	return DatetimeSleep[E, C, P]{info, kind, arg, clock, calendar}, err
}
func (x DatetimeSleep[E, C, P]) Reconstruct(info Info[P], arg E, clock sqltype.Usage) (DatetimeSleep[E, C, P], error) {
	calendar := x.Calendar
	if x.Kind == DatetimeSleepInterval {
		var err error
		calendar, err = sqltype.BindArithmetic(sqltype.ArithmeticAdd, clock.Type, arg.ResultUsage().Type, clock.Type)
		if err != nil {
			return x, err
		}
	}
	x.Info, x.Arg, x.ClockUsage, x.Calendar = info, arg, clock, calendar
	return x, nil
}
func DatetimeSleepIntrinsicRule() IntrinsicPropertyRule { return VolatileOnly }
