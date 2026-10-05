package stuntgres

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"

	"stuntgres/internal/engine"
	"stuntgres/internal/sqltype"
)

// Normalize before acquiring the engine lock: conversion may invoke user Valuer
// code. Byte arguments acquire immutable ownership exactly once here.
func normalizeArguments(args []driver.NamedValue) ([]driver.Value, error) {
	values := make([]driver.Value, len(args))
	for i, a := range args {
		if a.Name != "" {
			return nil, fmt.Errorf("stuntgres: named parameters are not supported")
		}
		if a.Ordinal != i+1 {
			return nil, fmt.Errorf("stuntgres: parameter ordinal %d, want %d", a.Ordinal, i+1)
		}
		v, err := driver.DefaultParameterConverter.ConvertValue(a.Value)
		if err != nil {
			return nil, err
		}
		switch v := v.(type) {
		case nil, int64, float64, string, bool:
			values[i] = v
		case []byte:
			values[i] = string(v)
		default:
			return nil, &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported parameter value"}
		}
	}
	return values, nil
}

func importParameter(ctx context.Context, raw driver.Value, target sqltype.Usage) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if raw == nil {
		return sqltype.Null(target.Type), ctx.Err()
	}
	var text string
	switch v := raw.(type) {
	case string:
		text = v
	case int64:
		if target.Type.ID() == sqltype.Int8 || (target.Type.ID() == sqltype.Int4 && v >= -2147483648 && v <= 2147483647) {
			return sqltype.Integer(target.Type, v)
		}
		// Overflow retains the parameter input diagnostic, not assignment narrowing.
		text = strconv.FormatInt(v, 10)
	case float64:
		text = strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if target.Type.ID() == sqltype.Bool {
			return sqltype.Boolean(target.Type, v), nil
		}
		text = strconv.FormatBool(v)
	default:
		return sqltype.Value{}, &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported parameter value"}
	}
	return target.Type.Input(ctx, text, -1)
}

// Only checked scalar imports and codecs run in this callback. Values are already owned and no user code
// runs under the coherent bind/import/evaluate publication critical section.
func frameImporter(values []driver.Value) engine.FrameImporter {
	return func(ctx context.Context, parameters engine.ParameterInfo, dst []sqltype.Value) error {
		for i, raw := range values {
			value, err := importParameter(ctx, raw, parameters.Types[i])
			if err != nil {
				return err
			}
			dst[i] = value
		}
		return nil
	}
}

func exportValue(ctx context.Context, v sqltype.Value) (driver.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !v.Type().Concrete() {
		return nil, fmt.Errorf("stuntgres: invalid result type")
	}
	if v.IsNull() {
		return nil, nil
	}
	switch v.Type().ID() {
	case sqltype.Int4, sqltype.Int8:
		return v.Integer()
	case sqltype.Float4, sqltype.Float8:
		return v.Float()
	case sqltype.Text, sqltype.Name:
		return v.Text()
	case sqltype.Bool:
		return v.Boolean()
	}
	return nil, fmt.Errorf("stuntgres: unsupported result type")
}

func scanType(d *sqltype.Descriptor) reflect.Type {
	switch d.ID() {
	case sqltype.Int4, sqltype.Int8:
		return reflect.TypeFor[int64]()
	case sqltype.Float4, sqltype.Float8:
		return reflect.TypeFor[float64]()
	case sqltype.Text, sqltype.Name:
		return reflect.TypeFor[string]()
	case sqltype.Bool:
		return reflect.TypeFor[bool]()
	}
	return nil
}
