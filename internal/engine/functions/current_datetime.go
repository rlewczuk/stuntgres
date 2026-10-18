package functions

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type CurrentDateTime struct {
	expr.Info
	Kind         parser.CurrentDateTimeKind
	HasPrecision bool
	Precision    int32
}

func CurrentDateTimeType(kind parser.CurrentDateTimeKind) sqltype.ID {
	switch kind {
	case parser.CurrentDate:
		return sqltype.Date
	case parser.CurrentTime:
		return sqltype.Timetz
	case parser.CurrentTimestamp:
		return sqltype.Timestamptz
	case parser.LocalTime:
		return sqltype.Time
	case parser.LocalTimestamp:
		return sqltype.Timestamp
	}
	return 0
}
func CurrentDateTimeShape(raw parser.CurrentDateTimeExpr) bool {
	return CurrentDateTimeType(raw.Kind) != 0 && (!raw.HasPrecision || raw.Kind != parser.CurrentDate && raw.Precision >= 0)
}
func ResolveCurrentDateTimeUsage(raw parser.CurrentDateTimeExpr, usage sqltype.Usage) (sqltype.Usage, *sqlstate.Error, error) {
	if raw.HasPrecision {
		return usage.Type.ResolveModifiers(raw.Precision)
	}
	return usage, nil, nil
}
func (e CurrentDateTime) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.CurrentDateTime(e.Usage, c.FunctionInput())
}
func (e CurrentDateTime) Validate(intrinsicMatch bool) error {
	id := CurrentDateTimeType(e.Kind)
	if id == 0 || e.Usage.Type == nil || e.Usage.Type.ID() != id || !e.Usage.Valid() || !intrinsicMatch {
		return fmt.Errorf("stuntgres: invalid current datetime expression")
	}
	precision := int32(-1)
	if e.HasPrecision {
		if e.Kind == parser.CurrentDate || e.Precision < 0 {
			return fmt.Errorf("stuntgres: invalid current datetime precision")
		}
		precision = min(e.Precision, 6)
	}
	if e.Usage.Typmod != precision {
		return fmt.Errorf("stuntgres: invalid current datetime typmod")
	}
	return nil
}
func CurrentDateTimeIntrinsicRule() IntrinsicPropertyRule { return ExactStable }

// CurrentDateTimeSQL renders the SQL value function for the engine's deparse
// and DEFAULT switches, until step 3 F2 gives the node a render hook.
func CurrentDateTimeSQL(e CurrentDateTime) (string, error) {
	if err := e.Validate(expr.PropertiesMatch(e.Property, CurrentDateTimeIntrinsicRule())); err != nil {
		return "", err
	}
	name := e.Kind.Name()
	if e.HasPrecision {
		name += "(" + strconv.FormatInt(int64(e.Precision), 10) + ")"
	}
	return name, nil
}

// CurrentDateTime implements expr.Composite as a leaf: no children, and Rebuild only takes
// the new metadata.
var _ expr.Composite = CurrentDateTime{}

func (e CurrentDateTime) Children() []expr.Expr { return nil }
func (e CurrentDateTime) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("CurrentDateTime", children, 0); err != nil {
		return nil, err
	}
	e.Info = info
	return e, nil
}

// IntrinsicVolatility is the analyzed volatility: the call is volatile or
// has no children, so nothing from eliminated arms can be retained.
func (e CurrentDateTime) IntrinsicVolatility() expr.Volatility { return e.Property.Volatility }

func (e CurrentDateTime) PlanningFold() expr.Fold { return expr.FoldNever }
