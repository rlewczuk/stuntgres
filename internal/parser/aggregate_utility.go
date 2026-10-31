package parser

import "reflect"

// aggregateUtilityArguments normalizes the shared declaration/utility identity.
func (l *lexer) aggregateUtilityArguments(direct, ordered []GrantRoutineArgument) ([]GrantRoutineArgument, bool) {
	for _, arguments := range [][]GrantRoutineArgument{direct, ordered} {
		for _, argument := range arguments {
			if argument.Mode == "out" || argument.Mode == "inout" {
				l.failAt("0A000", "aggregates cannot have output arguments", argument.Pos.Offset)
				return nil, false
			}
		}
	}
	if len(direct) != 0 && len(ordered) != 0 && direct[len(direct)-1].Mode == "variadic" {
		last, first := direct[len(direct)-1], ordered[0]
		if len(ordered) != 1 || first.Mode != "variadic" || !aggregateUtilitySameType(last.Type, first.Type) {
			l.failAt("0A000", "an ordered-set aggregate with a VARIADIC direct argument must have one VARIADIC aggregated argument of the same data type", first.Pos.Offset)
			return nil, false
		}
		return direct, true
	}
	return append(direct, ordered...), true
}

func aggregateUtilitySameType(a, b TypeName) bool {
	return aggregateUtilitySameRawType(reflect.ValueOf(a), reflect.ValueOf(b), true)
}

// A read-only view of PostgreSQL's raw names list. Reflection accessors also
// work beneath private TypeModifier.syntax; Interface would panic there.
type aggregateUtilityTypeNames struct {
	parts reflect.Value
	name, schema string
	system, defaultLength, hasSchema bool
}

func aggregateUtilityNames(raw reflect.Value) aggregateUtilityTypeNames {
	v := aggregateUtilityTypeNames{parts: raw.FieldByName("Parts"), name: raw.FieldByName("Name").String(), system: raw.FieldByName("System").Bool()}
	schema := raw.FieldByName("Schema")
	v.hasSchema = !schema.IsNil()
	if v.hasSchema { v.schema = schema.Elem().FieldByName("Text").String() }
	if raw.FieldByName("PctType").Bool() { v.system = false; return v }
	if v.system { return v }
	if v.parts.Len() > 1 || !schema.IsNil() { return v }
	if v.parts.Len() == 1 { v.name = v.parts.Index(0).FieldByName("Text").String() }
	if raw.FieldByName("Quoted").Bool() { return v }
	v.defaultLength = v.name == "bit" || v.name == "char" || v.name == "character"
	canonical := standaloneType(TypeName{Name: v.name})
	v.name, v.system = canonical.Name, canonical.System
	return v
}

func (v aggregateUtilityTypeNames) count() int {
	if v.system { return 2 }
	if v.parts.Len() != 0 { return v.parts.Len() }
	if v.hasSchema { return 2 }
	return 1
}

func (v aggregateUtilityTypeNames) part(i int) string {
	if v.system {
		if i == 0 { return "pg_catalog" }
		return v.name
	}
	if v.parts.Len() != 0 { return v.parts.Index(i).FieldByName("Text").String() }
	if v.hasSchema && i == 0 { return v.schema }
	return v.name
}

func aggregateUtilitySameRawType(a, b reflect.Value, typename bool) bool {
	an, bn := aggregateUtilityNames(a), aggregateUtilityNames(b)
	if an.count() != bn.count() { return false }
	for i := range an.count() { if an.part(i) != bn.part(i) { return false } }
	for _, field := range [...]string{"Setof", "PctType", "ArrayBounds"} {
		if !aggregateUtilitySameSyntax(a.FieldByName(field), b.FieldByName(field)) { return false }
	}
	am, bm := a.FieldByName("Modifiers"), b.FieldByName("Modifiers")
	ac, bc := am.Len(), bm.Len()
	ad, bd := typename && an.defaultLength && ac == 0, typename && bn.defaultLength && bc == 0
	if ad { ac = 1 }; if bd { bc = 1 }
	if ac != bc { return false }
	for i := range ac {
		var av, bv reflect.Value
		if !ad { av = am.Index(i) }; if !bd { bv = bm.Index(i) }
		if !aggregateUtilitySameModifier(av, bv) { return false }
	}
	return true
}

// An invalid modifier is the SQL BIT/CHAR Typename default, the raw integer 1.
func aggregateUtilityModifierInteger(m reflect.Value) (int64, bool) {
	if !m.IsValid() { return 1, true }
	syntax := m.FieldByName("syntax")
	if !syntax.IsNil() {
		v := syntax.Elem()
		if v.Type() != reflect.TypeFor[LiteralExpr]() { return 0, false }
		value := v.FieldByName("Value")
		return value.FieldByName("Integer").Int(), ValueKind(value.FieldByName("Kind").Uint()) == IntegerValue
	}
	integer := m.FieldByName("Integer")
	if integer.IsNil() || m.FieldByName("UnaryPlus").Bool() || m.FieldByName("NonSimple").Bool() { return 0, false }
	return integer.Elem().Int(), true
}

func aggregateUtilitySameModifier(a, b reflect.Value) bool {
	ai, aok := aggregateUtilityModifierInteger(a)
	bi, bok := aggregateUtilityModifierInteger(b)
	if aok || bok { return aok && bok && ai == bi }
	if !a.IsValid() || !b.IsValid() { return false }
	as, bs := a.FieldByName("syntax"), b.FieldByName("syntax")
	if !as.IsNil() || !bs.IsNil() {
		return aggregateUtilitySameSyntax(as, bs)
	}
	for _, field := range [...]string{"Integer", "Raw", "UnaryPlus", "NonSimple"} {
		if !aggregateUtilitySameSyntax(a.FieldByName(field), b.FieldByName(field)) { return false }
	}
	return true
}

// Compare PostgreSQL parsed syntax, not native parser/executor provenance.
// Native nodes retain the operand/list tree, but also carry capability and
// spelling markers that PostgreSQL's raw nodes do not. Keep their exclusions
// typed and local below; never ignore arbitrary fields by name. This does not
// bind, evaluate, serialize or copy the temporary modifier trees.
func aggregateUtilitySameSyntax(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}
	if a.Type() != b.Type() {
		return false
	}
	if a.Type() == reflect.TypeFor[Position]() {
		return true
	}
	if a.Type() == reflect.TypeFor[CastSyntax]() {
		// CAST and :: both construct TypeCast. Function-style casts instead
		// represent FuncCall and must remain distinct, even for equal values.
		return (CastSyntax(a.Uint()) == FunctionCast) == (CastSyntax(b.Uint()) == FunctionCast)
	}
	if a.Type() == reflect.TypeFor[TypeName]() {
		return aggregateUtilitySameRawType(a, b, true)
	}
	switch a.Kind() {
	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return aggregateUtilitySameSyntax(a.Elem(), b.Elem())
	case reflect.Struct:
		for i := range a.NumField() {
			if aggregateUtilitySyntaxProvenance(a.Type(), a.Type().Field(i).Name) {
				continue
			}
			if a.Type() == reflect.TypeFor[TypedLiteralExpr]() && a.Type().Field(i).Name == "TypeName" {
				if !aggregateUtilitySameRawType(a.Field(i), b.Field(i), false) { return false }
				continue
			}
			if !aggregateUtilitySameSyntax(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := range a.Len() {
			if !aggregateUtilitySameSyntax(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.String:
		return a.String() == b.String()
	case reflect.Bool:
		return a.Bool() == b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() == b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return a.Uint() == b.Uint()
	default:
		panic("unexpected aggregate modifier syntax kind: " + a.Kind().String())
	}
}

// These fields have no counterpart in PostgreSQL raw-expression identity.
// Other syntax discriminators are deliberately retained: SQL function formats,
// explicit ROW constructors, bare window references and comma joins can change
// the parsed node even when the resulting values would happen to agree.
func aggregateUtilitySyntaxProvenance(node reflect.Type, field string) bool {
	switch node {
	case reflect.TypeFor[ValuesQuery]():
		return field == "rowEnd" // Lexer replay boundary.
	case reflect.TypeFor[Select]():
		return field == "Parenthesized" // Operand grouping is already in Set.
	case reflect.TypeFor[FunctionExpr]():
		switch field {
		case "directSelectedCall", "ResolutionOnly", "CompositeCandidate",
			"StoredSQLRoutine", "StoredProviderRoutine", "StoredProviderSymbol",
			"All": // Explicit ALL is the default, not agg_distinct.
			return true
		}
	case reflect.TypeFor[AggregateExpr]():
		return field == "StoredAggregate" || field == "StoredProviderRoutine" || field == "StoredProviderSymbol"
	case reflect.TypeFor[CastExpr]():
		return field == "FrozenMethod" || field == "FrozenContext"
	case reflect.TypeFor[TypedLiteralExpr]():
		return field == "RequiresDomain" // Native capability classification.
	case reflect.TypeFor[SQLJSONQueryExpr]():
		return field == "TableColumn" // Analysis-only diagnostic context.
	case reflect.TypeFor[GroupClause]():
		return field == "All" // Like omitted ALL, groupDistinct is false.
	}
	return false
}
