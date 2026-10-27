package proc

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// routineDeclarationBody privately owns parsed release declaration syntax. It is
// deliberately not a RoutineProgram: parsing grants no execution or dependency
// authority, and catalog reads never bind or prepare these statements.
type routineDeclarationBody struct{ statements []parser.Statement }

func NewRoutineDeclarationBody(source string) (sqltype.StructuralTree, error) {
	statements, err := parser.ParseRoutineDeclarationBody(source)
	if err != nil {
		return nil, err
	}
	if len(statements) == 0 {
		return nil, fmt.Errorf("stuntgres: empty routine declaration body")
	}
	return &routineDeclarationBody{statements: statements}, nil
}

func (b *routineDeclarationBody) StructuralOutput(ctx context.Context) (string, error) {
	var out strings.Builder
	out.WriteString("{RoutineDeclarationBody")
	for _, statement := range b.statements {
		out.WriteByte(' ')
		if err := appendRoutineSyntax(ctx, &out, reflect.ValueOf(statement)); err != nil {
			return "", err
		}
	}
	out.WriteByte('}')
	return out.String(), nil
}

// AppendRoutineSyntax writes genuine parsed routine command structure without
// binding, evaluating or rendering SQL.
func AppendRoutineSyntax(ctx context.Context, out *strings.Builder, statement parser.Statement) error {
	return appendRoutineSyntax(ctx, out, reflect.ValueOf(statement))
}

// The parser's closed syntax is structs, ordered slices and scalar fields.
// Recording concrete node and field names preserves distinctions hidden by plain
// JSON interface encoding. Source positions are presentation, not tree identity.
func appendRoutineSyntax(ctx context.Context, out *strings.Builder, value reflect.Value) error {
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
		return appendRoutineSyntax(ctx, out, value.Elem())
	}
	switch value.Kind() {
	case reflect.Struct:
		out.WriteByte('{')
		out.WriteString(value.Type().String())
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if !field.IsExported() || field.Type == reflect.TypeFor[parser.Position]() {
				continue
			}
			out.WriteByte(' ')
			out.WriteString(field.Name)
			out.WriteByte(' ')
			if err := appendRoutineSyntax(ctx, out, value.Field(i)); err != nil {
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
			if err := appendRoutineSyntax(ctx, out, value.Index(i)); err != nil {
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
