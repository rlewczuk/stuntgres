package engine

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// ViewParts must mirror every viewDefinition field, and NewView and Parts
// must copy each one into its own twin: a field set alone round-trips alone.
func TestViewPartsRoundTrip(t *testing.T) {
	definition := reflect.TypeFor[viewDefinition]()
	parts := reflect.TypeFor[ViewParts]()
	if definition.NumField() != parts.NumField() {
		t.Fatalf("ViewParts has %d fields, viewDefinition %d", parts.NumField(), definition.NumField())
	}
	for i := range definition.NumField() {
		field := definition.Field(i)
		twin, ok := parts.FieldByName(strings.ToUpper(field.Name[:1]) + field.Name[1:])
		if !ok || twin.Type != field.Type {
			t.Fatalf("viewDefinition.%s has no ViewParts twin of type %s", field.Name, field.Type)
		}
	}
	for i := range parts.NumField() {
		var p ViewParts
		value := reflect.ValueOf(&p).Elem().Field(i)
		value.Set(nonZero(t, value.Type()))
		if got := NewView(p).Parts(); !reflect.DeepEqual(got, p) {
			t.Fatalf("ViewParts.%s did not round-trip: %+v", parts.Field(i).Name, got)
		}
		original := NewView(p)
		edited := original.With(func(q *ViewParts) { *q = ViewParts{} })
		if edited == original || !reflect.DeepEqual(original.Parts(), p) || !reflect.DeepEqual(edited.Parts(), ViewParts{}) {
			t.Fatalf("With changed its receiver for ViewParts.%s", parts.Field(i).Name)
		}
	}
}

// nonZero returns a value of typ that differs from its zero value.
func nonZero(t *testing.T, typ reflect.Type) reflect.Value {
	t.Helper()
	if typ == reflect.TypeFor[ids.SessionToken]() {
		return reflect.ValueOf(new(ids.Domain).Session(1))
	}
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
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(int64(1)).Convert(typ)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(uint64(1)).Convert(typ)
	case reflect.Interface:
		if v := reflect.ValueOf(parser.LiteralExpr{}); v.Type().Implements(typ) {
			return v.Convert(typ)
		}
	case reflect.Struct:
		v := reflect.New(typ).Elem()
		for i := range typ.NumField() {
			if typ.Field(i).IsExported() {
				v.Field(i).Set(nonZero(t, typ.Field(i).Type))
				return v
			}
		}
	}
	t.Fatalf("no non-zero value for %s", typ)
	return reflect.Value{}
}
