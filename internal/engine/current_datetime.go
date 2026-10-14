package engine

import (
	"fmt"
	"strconv"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

func (b *expressionBinder) bindCurrentDateTime(raw parser.CurrentDateTimeExpr) (BoundExpr, error) {
	if !functions.CurrentDateTimeShape(raw) {
		return nil, fmt.Errorf("stuntgres: invalid current datetime expression")
	}
	usage, warning, err := functions.ResolveCurrentDateTimeUsage(raw, b.typ(functions.CurrentDateTimeType(raw.Kind)).Usage())
	if err != nil {
		return nil, err
	}
	// A held description already analyzed this syntax. Execute must still
	// bind fresh descriptors and evaluate fresh transaction/session values.
	if warning != nil && b.op != nil && b.expected == nil {
		b.op.emitNotice(levelWarning, *warning)
	}
	info := b.info(usage, raw.Pos, functionIntrinsicProperties(functions.CurrentDateTimeIntrinsicRule()))
	return functions.CurrentDateTime[*EvalContext, ExprProperties]{Info: functionInfo(info), Kind: raw.Kind, HasPrecision: raw.HasPrecision, Precision: usage.Typmod}, nil
}

func currentDateTimeSQL(e functions.CurrentDateTime[*EvalContext, ExprProperties]) (string, error) {
	if err := e.Validate(functionPropertiesMatch(e.Property, functions.CurrentDateTimeIntrinsicRule())); err != nil {
		return "", err
	}
	name := e.Kind.Name()
	if e.HasPrecision {
		name += "(" + strconv.FormatInt(int64(e.Precision), 10) + ")"
	}
	return name, nil
}
