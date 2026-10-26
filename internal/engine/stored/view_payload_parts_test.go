package stored

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// ViewParts must mirror every View field, and NewView and Parts
// must copy each one into its own twin: a field set alone round-trips alone.
func TestViewPartsRoundTrip(t *testing.T) {
	definition := reflect.TypeFor[View]()
	parts := reflect.TypeFor[ViewParts]()
	if definition.NumField() != parts.NumField() {
		t.Fatalf("ViewParts has %d fields, View %d", parts.NumField(), definition.NumField())
	}
	for i := range definition.NumField() {
		field := definition.Field(i)
		twin, ok := parts.FieldByName(strings.ToUpper(field.Name[:1]) + field.Name[1:])
		if !ok || twin.Type != field.Type {
			t.Fatalf("View.%s has no ViewParts twin of type %s", field.Name, field.Type)
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

func TestViewDependencyPurpose(t *testing.T) {
	domain := new(ids.Domain)
	aggregate := catalog.ExpressionDependency{Ref: domain.Ref(1, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90001}), Revision: 1}
	support := catalog.ExpressionDependency{Ref: domain.Ref(1, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90002}), Revision: 1, Purpose: catalog.AggregateSupportDependency}
	source := ViewLeaf{Ref: domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90003})}
	for _, mixed := range []bool{false, true} {
		for _, directFirst := range []bool{false, true} {
			dependencies := []catalog.ExpressionDependency{aggregate, support}
			if mixed {
				direct := support
				direct.Purpose = catalog.DefinitionDependency
				if directFirst {
					dependencies = append([]catalog.ExpressionDependency{direct}, dependencies...)
				} else {
					dependencies = append(dependencies, direct)
				}
			}
			before := append([]catalog.ExpressionDependency(nil), dependencies...)
			definition := NewView(ViewParts{Sources: []ViewLeaf{source}, Dependencies: dependencies})
			if !definition.DependsOn(aggregate.Ref.Address) || !definition.DependsOn(source.Ref.Address) {
				t.Fatal("written aggregate or source edge lost")
			}
			if got := definition.DependsOn(support.Ref.Address); got != mixed {
				t.Fatalf("mixed=%t directFirst=%t: support definition edge=%t", mixed, directFirst, got)
			}
			if !reflect.DeepEqual(definition.Dependencies(), before) {
				t.Fatal("semantic projection changed execution capture manifest")
			}
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
	case reflect.String:
		return reflect.ValueOf("x").Convert(typ)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(int64(1)).Convert(typ)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(uint64(1)).Convert(typ)
	case reflect.Interface:
		for _, candidate := range []any{parser.LiteralExpr{}, plan.LiteralExpr{}, &parser.Insert{}} {
			if v := reflect.ValueOf(candidate); v.Type().Implements(typ) {
				return v.Convert(typ)
			}
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
