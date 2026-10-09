package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type currentDateTimeExpression struct {
	expressionInfo
	kind         parser.CurrentDateTimeKind
	hasPrecision bool
	precision    int32
}

func currentDateTimeType(kind parser.CurrentDateTimeKind) sqltype.ID {
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

func (e currentDateTimeExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.CurrentDateTime(e.usage, c.Input)
}

func (b *expressionBinder) bindCurrentDateTime(raw parser.CurrentDateTimeExpr) (BoundExpr, error) {
	id := currentDateTimeType(raw.Kind)
	if id == 0 || raw.HasPrecision && (raw.Kind == parser.CurrentDate || raw.Precision < 0) {
		return nil, fmt.Errorf("stuntgres: invalid current datetime expression")
	}
	usage := b.typ(id).Usage()
	if raw.HasPrecision {
		resolved, warning, err := usage.Type.ResolveModifiers(raw.Precision)
		if err != nil {
			return nil, err
		}
		usage = resolved
		// A held description already analyzed this syntax. Execute must still
		// bind fresh descriptors and evaluate fresh transaction/session values.
		if warning != nil && b.op != nil && b.expected == nil {
			b.op.emitNotice(levelWarning, *warning)
		}
	}
	return currentDateTimeExpression{expressionInfo: b.info(usage, raw.Pos, ExprProperties{Volatility: Stable}), kind: raw.Kind, hasPrecision: raw.HasPrecision, precision: usage.Typmod}, nil
}

func (e currentDateTimeExpression) sql() (string, error) {
	if err := validateCurrentDateTime(e); err != nil {
		return "", err
	}
	name := e.kind.Name()
	if e.hasPrecision {
		name += "(" + strconv.FormatInt(int64(e.precision), 10) + ")"
	}
	return name, nil
}

func validateCurrentDateTime(e currentDateTimeExpression) error {
	id := currentDateTimeType(e.kind)
	if id == 0 || e.usage.Type == nil || e.usage.Type.ID() != id || !e.usage.Valid() || e.Properties() != (ExprProperties{Volatility: Stable}) {
		return fmt.Errorf("stuntgres: invalid current datetime expression")
	}
	precision := int32(-1)
	if e.hasPrecision {
		if e.kind == parser.CurrentDate || e.precision < 0 {
			return fmt.Errorf("stuntgres: invalid current datetime precision")
		}
		precision = min(e.precision, 6)
	}
	if e.usage.Typmod != precision {
		return fmt.Errorf("stuntgres: invalid current datetime typmod")
	}
	return nil
}
