package stuntgres

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"time"

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
		case nil, int64, float64, string, bool, time.Time:
			values[i] = v
		case []byte:
			if v == nil {
				values[i] = []byte(nil)
			} else {
				values[i] = string(v)
			}
		default:
			return nil, &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported parameter value"}
		}
	}
	return values, nil
}

func importParameter(ctx context.Context, raw driver.Value, target sqltype.Usage, input sqltype.InputEnvironment) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if raw == nil {
		return sqltype.Null(target.Type), ctx.Err()
	}
	var text string
	switch v := raw.(type) {
	case []byte:
		// Only typed nil survives normalization; mutable nonnil buffers never
		// cross the engine lock boundary.
		if v != nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: unnormalized byte argument")
		}
		if target.Type.ID() == sqltype.Bytea {
			return sqltype.Null(target.Type), nil
		}
	case time.Time:
		if target.Type.ID() == sqltype.Timestamp {
			return sqltype.TimestampFromTime(target.Type, v)
		}
		if target.Type.ID() == sqltype.Date {
			return sqltype.DateFromTime(target.Type, v)
		}
		// Preserve the existing text-input policy for other SQL destinations.
		text = v.Format("2006-01-02 15:04:05.999999999Z07:00")
	case string:
		if target.Type.ID() == sqltype.Bytea {
			return sqltype.Binary(target.Type, v)
		}
		text = v
	case int64:
		if target.Type.ID() == sqltype.Int8 || (target.Type.ID() == sqltype.Int4 && v >= -2147483648 && v <= 2147483647) || (target.Type.ID() == sqltype.Int2 && v >= -32768 && v <= 32767) {
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
	return target.Type.InputWithEnvironment(ctx, text, -1, input)
}

// Only checked scalar imports and codecs run in this callback. Values are already owned and no user code
// runs under the coherent bind/import/evaluate publication critical section.
func frameImporter(values []driver.Value) engine.FrameImporter {
	return func(ctx context.Context, parameters engine.ParameterInfo, dst []sqltype.Value, input sqltype.InputEnvironment) error {
		for i, raw := range values {
			value, err := importParameter(ctx, raw, parameters.Types[i], input)
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
	case sqltype.Bytea:
		raw, err := v.Binary()
		if err != nil {
			return nil, err
		}
		return []byte(raw), nil
	case sqltype.UUID, sqltype.Time, sqltype.Numeric, sqltype.Date:
		return v.Type().Output(ctx, v)
	case sqltype.Timestamp:
		n, err := v.Timestamp()
		if err != nil {
			return nil, err
		}
		if n == sqltype.TimestampNegativeInfinity {
			return []byte("-infinity"), nil
		}
		if n == sqltype.TimestampPositiveInfinity {
			return []byte("infinity"), nil
		}
		return v.TimestampTime()
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		return v.Integer()
	case sqltype.Float4, sqltype.Float8:
		return v.Float()
	case sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name:
		return v.Text()
	case sqltype.Bool:
		return v.Boolean()
	}
	return nil, fmt.Errorf("stuntgres: unsupported result type")
}

func scanType(d *sqltype.Descriptor) reflect.Type {
	switch d.ID() {
	case sqltype.Bytea:
		return reflect.TypeFor[[]byte]()
	case sqltype.Timestamp:
		return reflect.TypeFor[time.Time]()
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		return reflect.TypeFor[int64]()
	case sqltype.Float4, sqltype.Float8:
		return reflect.TypeFor[float64]()
	case sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name, sqltype.UUID, sqltype.Time, sqltype.Numeric, sqltype.Date:
		return reflect.TypeFor[string]()
	case sqltype.Bool:
		return reflect.TypeFor[bool]()
	}
	return nil
}
