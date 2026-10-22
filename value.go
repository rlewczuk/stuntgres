package stuntgres

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
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
	if target.Type.IsDomain() && !target.Type.BaseType().IsArray() {
		base := target.BaseUsage()
		value, err := importParameter(ctx, raw, base, input)
		if err != nil {
			return sqltype.Value{}, err
		}
		if base.Typmod != -1 && !value.IsNull() {
			plan, er := sqltype.PlanCoercion(base.Type.Usage(), base, sqltype.Assignment)
			if er != nil {
				return sqltype.Value{}, er
			}
			value, err = plan.Apply(ctx, value, input, sqltype.OutputEnvironment{})
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		if err := target.Type.CheckDomainInput(ctx, value, input); err != nil {
			return sqltype.Value{}, err
		}
		return target.Type.Wrap(value)
	}
	if raw == nil {
		value := sqltype.Null(target.Type)
		if err := target.Type.CheckDomainInput(ctx, value, input); err != nil {
			return sqltype.Value{}, err
		}
		return value, ctx.Err()
	}
	var text string
	switch v := raw.(type) {
	case []byte:
		// Only typed nil survives normalization; mutable nonnil buffers never
		// cross the engine lock boundary. Preserve the destination's NULL
		// policy before the legacy textual-input fallback below.
		if v != nil {
			return sqltype.Value{}, fmt.Errorf("stuntgres: unnormalized byte argument")
		}
		if target.Type.ContainsRegproc() {
			return sqltype.Null(target.Type), nil
		}
		if sqltype.IsFTS(target.Type.ID()) {
			return sqltype.Null(target.Type), nil
		}
		if target.Type.ID() == sqltype.Box || target.Type.ID() == sqltype.Point || target.Type.ID() == sqltype.Tid || target.Type.ID() == sqltype.Macaddr {
			return sqltype.Null(target.Type), nil
		}
		if target.Type.IsCitext() || target.Type.IsCube() || target.Type.IsQueryInt() || sqltype.IsTransactionIdentifier(target.Type.ID()) || sqltype.IsOperatorAlias(target.Type.ID()) || sqltype.IsRegObjectAlias(target.Type.ID()) || sqltype.IsRegTS(target.Type.ID()) {
			return sqltype.Null(target.Type), nil
		}
		if target.Type.ContainsACLItem() || sqltype.IsNetwork(target.Type.ID()) || target.Type.ID() == sqltype.Int4Multirange || target.Type.ID() == sqltype.Int8Multirange || target.Type.ID() == sqltype.NumMultirange || target.Type.ID() == sqltype.Int4Range || target.Type.ID() == sqltype.Int8Range || target.Type.ID() == sqltype.NumRange || target.Type.ID() == sqltype.DateRange || target.Type.ID() == sqltype.TimestampRange || target.Type.ID() == sqltype.TimestamptzRange || target.Type.ID() == sqltype.Money || target.Type.ID() == sqltype.Bytea || target.Type.ID() == sqltype.JSON || target.Type.ID() == sqltype.JSONB || target.Type.ID() == sqltype.JSONPath || target.Type.ID() == sqltype.Bit || target.Type.ID() == sqltype.VarBit {
			return sqltype.Null(target.Type), nil
		}
	case time.Time:
		if target.Type.ID() == sqltype.Timestamptz {
			return sqltype.TimestampFromTime(target.Type, v.UTC())
		}
		if target.Type.ID() == sqltype.Timestamp {
			return sqltype.TimestampFromTime(target.Type, v)
		}
		if target.Type.ID() == sqltype.Date {
			return sqltype.DateFromTime(target.Type, v)
		}
		if target.Type.ID() == sqltype.Timetz {
			text = timetzParameterText(v)
			break
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
	typmod := int32(-1)
	if target.Type.ID() == sqltype.Interval {
		typmod = target.Typmod
	}
	return target.Type.InputWithEnvironment(ctx, text, typmod, input)
}

// Keep TIMETZ on its checked textual codec, including optional-date validation
// and fractional rounding. Encode the actual offset sign before taking its
// magnitude: a minute-only Go layout loses the sign of offsets such as -30s.
func timetzParameterText(v time.Time) string {
	var buffer [64]byte
	text := v.AppendFormat(buffer[:0], "2006-01-02 15:04:05.999999999")
	_, offset := v.Zone()
	sign := byte('+')
	if offset < 0 {
		sign = '-'
		offset = -offset
	}
	text = append(text, sign)
	if offset/3600 < 10 {
		text = append(text, '0')
	}
	text = strconv.AppendInt(text, int64(offset/3600), 10)
	minutes, seconds := offset/60%60, offset%60
	text = append(text, ':', byte('0'+minutes/10), byte('0'+minutes%10))
	text = append(text, ':', byte('0'+seconds/10), byte('0'+seconds%10))
	return string(text)
}

// Arguments have already acquired immutable ownership; no Valuer/user callback
// runs here. Sequential codec/CHECK completion runs outside the engine mutex.
func frameImporter(values []driver.Value) engine.FrameImporter {
	return func(ctx context.Context, parameters analyze.ParameterInfo, dst []sqltype.Value, input sqltype.InputEnvironment) error {
		for i, raw := range values {
			parameterInput := input
			if text, ok := raw.(string); ok {
				parameterInput = input.ParameterInput(i+1, text == "")
			}
			value, err := importParameter(ctx, raw, parameters.Types[i], parameterInput)
			if err != nil {
				if diagnostic, ok := err.(*Error); ok {
					copy := *diagnostic
					if copy.Context != "" {
						copy.Context += "\n"
					}
					copy.Context += fmt.Sprintf("unnamed portal parameter $%d", i+1)
					if copy.Code != "22021" && raw != nil {
						if text, ok := raw.(string); ok && text == "" {
							copy.Context += " = ''"
						} else {
							copy.Context += " = '...'"
						}
					}
					return &copy
				}
				return err
			}
			dst[i] = value
		}
		return nil
	}
}

func exportValue(ctx context.Context, v sqltype.Value, output sqltype.OutputEnvironment) (driver.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if v.Type() == nil || !v.ValidResult(v.Type().Usage()) {
		return nil, fmt.Errorf("stuntgres: invalid result type")
	}
	if v.IsNull() {
		return nil, nil
	}
	if v.Type().IsDomain() {
		var err error
		v, err = v.BaseValue()
		if err != nil {
			return nil, err
		}
	}
	if v.Type().IsCitext() || v.Type().IsCube() || v.Type().IsQueryInt() || v.Type().IsArray() || v.Type().IsEnum() || v.Type().IsComposite() {
		return v.Type().OutputWithEnvironment(ctx, v, output)
	}
	switch v.Type().ID() {
	case sqltype.TSVector, sqltype.TSQuery, sqltype.Box, sqltype.Point:
		return v.Type().OutputWithEnvironment(ctx, v, output)
	case sqltype.ACLItem:
		return v.Type().OutputWithEnvironment(ctx, v, output)
	case sqltype.Tid, sqltype.Macaddr, sqltype.Inet, sqltype.Cidr:
		return v.Type().OutputWithEnvironment(ctx, v, output)
	case sqltype.Regtype, sqltype.Regproc, sqltype.Regprocedure, sqltype.Regrole, sqltype.Regoper, sqltype.Regoperator, sqltype.Regcollation, sqltype.Regnamespace, sqltype.Regconfig, sqltype.Regdictionary:
		return v.Type().OutputWithEnvironment(ctx, v, output)
	case sqltype.Xid, sqltype.Cid, sqltype.Xid8:
		text, err := v.Type().Output(ctx, v)
		return []byte(text), err
	case sqltype.Oid:
		n, err := v.OID()
		return int64(n), err
	case sqltype.Bytea:
		raw, err := v.Binary()
		if err != nil {
			return nil, err
		}
		return []byte(raw), nil
	case sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange:
		return v.Type().OutputWithEnvironment(ctx, v, output)
	case sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange, sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange, sqltype.Money, sqltype.Bit, sqltype.VarBit, sqltype.JSON, sqltype.JSONB, sqltype.JSONPath, sqltype.PgNodeTree, sqltype.Interval, sqltype.Void, sqltype.UUID, sqltype.Time, sqltype.Timetz, sqltype.Numeric, sqltype.Date, sqltype.Char:
		return v.Type().OutputWithEnvironment(ctx, v, output)
	case sqltype.Timestamp, sqltype.Timestamptz:
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
		result, err := v.TimestampTime()
		if err != nil {
			return nil, err
		}
		if v.Type().ID() == sqltype.Timestamptz {
			east, name := output.Zone.OffsetAt(result.Unix())
			result = result.In(time.FixedZone(name, int(east)))
		}
		return result, nil
	case sqltype.Int2, sqltype.Int4, sqltype.Int8:
		return v.Integer()
	case sqltype.Float4, sqltype.Float8:
		return v.Float()
	case sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name, sqltype.Regclass:
		return v.Text()
	case sqltype.Bool:
		return v.Boolean()
	}
	return nil, fmt.Errorf("stuntgres: unsupported result type")
}

func scanType(d *sqltype.Descriptor) reflect.Type {
	if d.Usage().CatalogNullUsage() {
		return reflect.TypeFor[any]()
	}
	if d.BaseType().IsCitext() || d.BaseType().IsCube() || d.BaseType().IsQueryInt() || d.BaseType().IsArray() || d.BaseType().IsEnum() || d.BaseType().IsComposite() || d.IsDeferredRecord() {
		return reflect.TypeFor[string]()
	}
	switch d.BaseType().ID() {
	case sqltype.TSVector, sqltype.TSQuery, sqltype.Box, sqltype.Point:
		return reflect.TypeFor[string]()
	case sqltype.ACLItem:
		return reflect.TypeFor[string]()
	case sqltype.Tid, sqltype.Macaddr, sqltype.Inet, sqltype.Cidr:
		return reflect.TypeFor[string]()
	case sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange:
		return reflect.TypeFor[string]()
	case sqltype.Bytea, sqltype.Xid, sqltype.Cid, sqltype.Xid8:
		return reflect.TypeFor[[]byte]()
	case sqltype.Timestamp, sqltype.Timestamptz:
		return reflect.TypeFor[time.Time]()
	case sqltype.Oid, sqltype.Int2, sqltype.Int4, sqltype.Int8:
		return reflect.TypeFor[int64]()
	case sqltype.Float4, sqltype.Float8:
		return reflect.TypeFor[float64]()
	case sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange, sqltype.DateRange, sqltype.TimestampRange, sqltype.TimestamptzRange, sqltype.Money, sqltype.Bit, sqltype.VarBit, sqltype.JSON, sqltype.JSONB, sqltype.JSONPath, sqltype.PgNodeTree, sqltype.Interval, sqltype.Void, sqltype.Text, sqltype.BPChar, sqltype.VarChar, sqltype.Name, sqltype.UUID, sqltype.Time, sqltype.Timetz, sqltype.Numeric, sqltype.Date, sqltype.Regclass, sqltype.Regtype, sqltype.Regproc, sqltype.Regprocedure, sqltype.Regrole, sqltype.Regoper, sqltype.Regoperator, sqltype.Regcollation, sqltype.Regnamespace, sqltype.Regconfig, sqltype.Regdictionary, sqltype.Char:
		return reflect.TypeFor[string]()
	case sqltype.Bool:
		return reflect.TypeFor[bool]()
	}
	return nil
}
