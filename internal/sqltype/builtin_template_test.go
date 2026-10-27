package sqltype

import (
	"reflect"
	"testing"
)

// descriptorFieldCount counts the fields of t, including fields of nested
// structs, that hold a *Descriptor.
func descriptorFieldCount(t reflect.Type) int {
	n := 0
	for i := range t.NumField() {
		switch f := t.Field(i).Type; {
		case f == reflect.TypeFor[*Descriptor]():
			n++
		case f.Kind() == reflect.Struct:
			n += descriptorFieldCount(f)
		}
	}
	return n
}

// descriptorTargets appends the non-nil *Descriptor fields of v.
func descriptorTargets(v reflect.Value, out []uintptr) []uintptr {
	switch {
	case v.Type() == reflect.TypeFor[*Descriptor]():
		if !v.IsNil() {
			out = append(out, v.Pointer())
		}
	case v.Kind() == reflect.Struct:
		for i := range v.NumField() {
			out = descriptorTargets(v.Field(i), out)
		}
	}
	return out
}

// Builtins copies its template: each call is a new generation equal to one
// built from the compiled tables, whose descriptor pointers stay inside it.
func TestBuiltinsCopyTemplate(t *testing.T) {
	if n := descriptorFieldCount(reflect.TypeFor[Descriptor]()); n != len((&Descriptor{}).descriptorPointers()) {
		t.Fatalf("Descriptor has %d descriptor pointer fields; descriptorPointers lists %d", n, len((&Descriptor{}).descriptorPointers()))
	}
	first, second := Builtins(), Builtins()
	if !reflect.DeepEqual(first, buildBuiltins()) {
		t.Fatal("a copied generation differs from a built one")
	}
	own := make(map[uintptr]bool, len(first))
	for i, d := range first {
		if d == second[i] || d == builtins().types[i] {
			t.Fatal("generations share a descriptor", d.Name())
		}
		own[reflect.ValueOf(d).Pointer()] = true
	}
	for _, d := range first {
		for _, target := range descriptorTargets(reflect.ValueOf(*d), nil) {
			if !own[target] {
				t.Fatalf("%s points outside its generation", d.Name())
			}
		}
	}
}
