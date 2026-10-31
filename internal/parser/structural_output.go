package parser

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// AppendStructuralSyntax writes genuine parsed routine command structure without
// binding, evaluating or rendering SQL.
func AppendStructuralSyntax(ctx context.Context, out *strings.Builder, statement Statement) error {
	return appendStructuralSyntax(ctx, out, reflect.ValueOf(statement))
}

// The parser's closed syntax is structs, ordered slices and scalar fields.
// Recording concrete node and field names preserves distinctions hidden by plain
// JSON interface encoding. Source positions are presentation, not tree identity.
func appendStructuralSyntax(ctx context.Context, out *strings.Builder, value reflect.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !value.IsValid() {
		out.WriteString("nil")
		return nil
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			out.WriteString("nil")
			return nil
		}
		return appendStructuralSyntax(ctx, out, value.Elem())
	}
	switch value.Kind() {
	case reflect.Struct:
		out.WriteByte('{')
		out.WriteString(value.Type().String())
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if !field.IsExported() || field.Type == reflect.TypeFor[Position]() {
				continue
			}
			out.WriteByte(' ')
			out.WriteString(field.Name)
			out.WriteByte(' ')
			if err := appendStructuralSyntax(ctx, out, value.Field(i)); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case reflect.Slice, reflect.Array:
		out.WriteByte('[')
		for i := range value.Len() {
			if i != 0 {
				out.WriteByte(' ')
			}
			if err := appendStructuralSyntax(ctx, out, value.Index(i)); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case reflect.String:
		out.WriteString(strconv.Quote(value.String()))
	case reflect.Bool:
		out.WriteString(strconv.FormatBool(value.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		out.WriteString(strconv.FormatInt(value.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		out.WriteString(strconv.FormatUint(value.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		out.WriteString(strconv.FormatFloat(value.Float(), 'g', -1, value.Type().Bits()))
	default:
		return fmt.Errorf("stuntgres: unsupported routine syntax field %s", value.Type())
	}
	return nil
}
