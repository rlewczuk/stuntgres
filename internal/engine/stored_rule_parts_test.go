package engine

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// RuleParts and RuleActionParts must mirror every field of their payloads,
// and the builders and Parts must copy each one into its own twin: a field
// set alone round-trips alone, and With never changes its receiver.
func TestRulePartsRoundTrip(t *testing.T) {
	checkPartsRoundTrip(t, NewRule, ruleDefinition.Parts, ruleDefinition.With)
	checkPartsRoundTrip(t, NewRuleAction, ruleAction.Parts, ruleAction.With)
}

func checkPartsRoundTrip[T, P any](t *testing.T, build func(P) T, parts func(T) P, with func(T, func(*P)) T) {
	t.Helper()
	payload, twins := reflect.TypeFor[T](), reflect.TypeFor[P]()
	if payload.NumField() != twins.NumField() {
		t.Fatalf("%s has %d fields, %s %d", twins, twins.NumField(), payload, payload.NumField())
	}
	for i := range payload.NumField() {
		field := payload.Field(i)
		twin, ok := twins.FieldByName(strings.ToUpper(field.Name[:1]) + field.Name[1:])
		if !ok || twin.Type != field.Type {
			t.Fatalf("%s.%s has no %s twin of type %s", payload, field.Name, twins, field.Type)
		}
	}
	var zero P
	for i := range twins.NumField() {
		var p P
		value := reflect.ValueOf(&p).Elem().Field(i)
		value.Set(ruleNonZero(t, value.Type()))
		if got := parts(build(p)); !reflect.DeepEqual(got, p) {
			t.Fatalf("%s.%s did not round-trip: %+v", twins, twins.Field(i).Name, got)
		}
		original := build(p)
		edited := with(original, func(q *P) { *q = zero })
		if !reflect.DeepEqual(parts(original), p) || !reflect.DeepEqual(parts(edited), zero) {
			t.Fatalf("With changed its receiver for %s.%s", twins, twins.Field(i).Name)
		}
	}
}

// ruleNonZero returns a value of typ that differs from its zero value.
func ruleNonZero(t *testing.T, typ reflect.Type) reflect.Value {
	t.Helper()
	switch typ.Kind() {
	case reflect.Pointer:
		return reflect.New(typ.Elem())
	case reflect.Slice:
		return reflect.MakeSlice(typ, 1, 1)
	case reflect.Map:
		m := reflect.MakeMap(typ)
		m.SetMapIndex(reflect.Zero(typ.Key()), reflect.Zero(typ.Elem()))
		return m
	case reflect.Bool:
		return reflect.ValueOf(true).Convert(typ)
	case reflect.String:
		return reflect.ValueOf("x").Convert(typ)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(uint64(1)).Convert(typ)
	case reflect.Interface:
		for _, candidate := range []any{plan.LiteralExpr{}, &parser.Insert{}} {
			if v := reflect.ValueOf(candidate); v.Type().Implements(typ) {
				return v.Convert(typ)
			}
		}
	case reflect.Struct:
		v := reflect.New(typ).Elem()
		for i := range typ.NumField() {
			if typ.Field(i).IsExported() {
				v.Field(i).Set(ruleNonZero(t, typ.Field(i).Type))
				return v
			}
		}
	}
	t.Fatalf("no non-zero value for %s", typ)
	return reflect.Value{}
}
