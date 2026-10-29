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
type routineDeclarationBody struct {
	statements []parser.Statement
	recipe     DeclarationRecipe
}

func NewRoutineDeclarationBody(recipe DeclarationRecipe) (sqltype.StructuralTree, error) {
	source := recipe.source()
	if recipe.Source != "" {
		source = recipe.Source
	}
	statements, err := parser.ParseRoutineDeclarationBody(source)
	if err != nil {
		return nil, err
	}
	if len(statements) != 1 || len(recipe.Parts) == 0 {
		return nil, fmt.Errorf("stuntgres: invalid routine declaration recipe")
	}
	recipe.Parts = append([]DeclarationPart(nil), recipe.Parts...)
	return &routineDeclarationBody{statements: statements, recipe: recipe}, nil
}

// DeclarationParts exposes immutable values without copying the retained list
// or leaking mutable parser syntax.
type DeclarationParts struct{ body *routineDeclarationBody }

func (p DeclarationParts) Return() bool             { return p.body.recipe.Return }
func (p DeclarationParts) Len() int                 { return len(p.body.recipe.Parts) }
func (p DeclarationParts) At(i int) DeclarationPart { return p.body.recipe.Parts[i] }

func RoutineDeclarationRecipe(tree sqltype.StructuralTree) (DeclarationParts, bool) {
	body, ok := tree.(*routineDeclarationBody)
	return DeclarationParts{body: body}, ok && body != nil
}

// EarthDeclarationCubeSchema reads placement from the private parsed owner.
// It grants no lookup or execution authority and never exposes mutable syntax.
// Callers must independently establish the sealed released declaration identity.
func EarthDeclarationCubeSchema(tree sqltype.StructuralTree, op sqltype.EarthOperation) (string, error) {
	invalid := func() (string, error) { return "", fmt.Errorf("stuntgres: invalid Earth declaration placement") }
	body, ok := tree.(*routineDeclarationBody)
	if !ok || body == nil || len(body.statements) != 1 {
		return invalid()
	}
	query, ok := body.statements[0].(*parser.Select)
	if !ok || len(query.Targets) != 1 || query.From != nil || query.Values != nil || len(query.With) != 0 || query.Where != nil || query.Set != nil {
		return invalid()
	}
	name, count := "cube_ll_coord", 0
	switch op {
	case sqltype.EarthLL:
		if _, ok := query.Targets[0].(parser.CastExpr); !ok {
			return invalid()
		}
		name, count = "cube", 3
	case sqltype.EarthLatitude:
		c, ok := query.Targets[0].(parser.CaseExpr)
		if !ok || c.Test != nil || len(c.Arms) != 2 || c.Else == nil {
			return invalid()
		}
		count = 3
	case sqltype.EarthLongitude:
		call, ok := query.Targets[0].(parser.FunctionExpr)
		if !ok || len(call.Name.Parts) != 1 || call.Name.Parts[0].Text != "degrees" || len(call.Args) != 1 {
			return invalid()
		}
		count = 2
	default:
		return invalid()
	}
	schema, calls, valid := "", 0, true
	var visit func(parser.Expr)
	visit = func(value parser.Expr) {
		if !valid {
			return
		}
		switch call := value.(type) {
		case parser.FunctionExpr:
			parts := call.Name.Parts
			if len(parts) == 0 {
				valid = false
				return
			}
			last := parts[len(parts)-1].Text
			if last == "cube" || last == "cube_ll_coord" {
				arity := 2
				if name == "cube" && calls == 2 {
					arity = 1
				}
				if last != name || len(parts) != 2 || parts[0].Text == "" || len(call.Args) != arity || call.ProcedureCall || call.Variadic || len(call.ArgNames) != 0 {
					valid = false
					return
				}
				if calls != 0 && schema != parts[0].Text {
					valid = false
					return
				}
				schema = parts[0].Text
				calls++
			} else {
				allowed := false
				switch op {
				case sqltype.EarthLL:
					allowed = last == "earth" || last == "cos" || last == "sin" || last == "radians"
				case sqltype.EarthLatitude:
					allowed = last == "earth" || last == "degrees" || last == "asin"
				case sqltype.EarthLongitude:
					allowed = last == "degrees" || last == "atan2"
				}
				if len(parts) != 1 || !allowed {
					valid = false
					return
				}
			}
			for _, arg := range call.Args {
				visit(arg)
			}
		case parser.CastExpr:
			visit(call.Child)
		case parser.OperatorExpr:
			if len(call.Name.Parts) != 1 || len(call.Args) != 2 {
				valid = false
				return
			}
			switch call.Name.Parts[0].Text {
			case "*", "/", "<", ">":
				for _, arg := range call.Args {
					visit(arg)
				}
			default:
				valid = false
			}
		case parser.CaseExpr:
			if op != sqltype.EarthLatitude || call.Test != nil || len(call.Arms) != 2 || call.Else == nil {
				valid = false
				return
			}
			for _, arm := range call.Arms {
				visit(arm.Condition)
				visit(arm.Result)
			}
			visit(call.Else)
		case parser.ParameterExpr:
			valid = call.Number == 1 || op == sqltype.EarthLL && call.Number == 2
		case parser.LiteralExpr:
		default:
			valid = false
		}
	}
	visit(query.Targets[0])
	if !valid || calls != count {
		return invalid()
	}
	return schema, nil
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
