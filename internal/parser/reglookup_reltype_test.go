package parser

import (
	"context"
	"testing"
)

func TestRegLookupRelTypeDeclarationDefaults(t *testing.T) {
	for _, tc := range []struct {
		input, name         string
		modifier            int32
		hasModifier, system bool
		arrays              int
	}{
		{"char", "bpchar", 1, true, true, 0},
		{"character", "bpchar", 1, true, true, 0},
		{"char[]", "bpchar", 1, true, true, 1},
		{"character[3]", "bpchar", 1, true, true, 1},
		{"char(3)", "bpchar", 3, true, true, 0},
		{"character(4)[]", "bpchar", 4, true, true, 1},
		{"bit", "bit", 1, true, true, 0},
		{"bit[]", "bit", 1, true, true, 1},
		{"bit(7)[]", "bit", 7, true, true, 1},
		{"bpchar", "bpchar", 0, false, false, 0},
		{"pg_catalog.bpchar", "bpchar", 0, false, false, 0},
		{"pg_catalog.bit", "bit", 0, false, false, 0},
		{`"char"`, "char", 0, false, false, 0},
		{`"bit"[]`, "bit", 0, false, false, 1},
		{"varbit", "varbit", 0, false, false, 0},
		{"character varying", "varchar", 0, false, true, 0},
		{"bit varying", "varbit", 0, false, true, 0},
	} {
		t.Run(tc.input, func(t *testing.T) {
			raw, err := ParseTypeName(context.Background(), tc.input, nil)
			if err != nil {
				t.Fatal(err)
			}
			if raw.Name != tc.name || raw.System != tc.system || len(raw.ArrayBounds) != tc.arrays {
				t.Fatal("declaration normalization changed identity/arrays", raw)
			}
			if !tc.hasModifier {
				if len(raw.Modifiers) != 0 {
					t.Fatal("generic/quoted name acquired keyword modifier", raw)
				}
			} else if len(raw.Modifiers) != 1 || raw.Modifiers[0].Integer == nil || *raw.Modifiers[0].Integer != tc.modifier {
				t.Fatal("implicit/explicit modifier was lost or overwritten", raw)
			}
		})
	}
	for _, tc := range []struct {
		input            string
		modifierPosition Position
	}{
		{"  char[4]", Position{Offset: 2, Character: 3}},
		{"  char(3)[4]", Position{Offset: 7, Character: 8}},
		{"  bit[4]", Position{Offset: 2, Character: 3}},
		{"  bit(3)[4]", Position{Offset: 6, Character: 7}},
	} {
		raw, err := ParseTypeName(context.Background(), tc.input, nil)
		if err != nil || raw.Pos != (Position{Offset: 2, Character: 3}) || len(raw.Modifiers) != 1 || raw.Modifiers[0].Pos != tc.modifierPosition || len(raw.ArrayBounds) != 1 || raw.ArrayBounds[0] != 4 {
			t.Fatal("final normalization lost declaration/modifier positions or array bounds", tc.input, raw, err)
		}
	}
	early := standaloneType(TypeName{Name: "char", Pos: Position{Character: 1}})
	if !early.System || early.Name != "bpchar" || len(early.Modifiers) != 0 {
		t.Fatal("early alias admission manufactured a declaration default", early)
	}
	// The final decoded declaration normalization must not mutate ordinary
	// SQL annotation admission or manufacture modifiers there.
	statements, err := Parse("CREATE TABLE t(a char,b bit,c varchar)")
	if err != nil {
		t.Fatal(err)
	}
	create := statements[0].(*CreateTable)
	for _, element := range create.Elements {
		if len(element.Column.TypeName.Modifiers) != 0 {
			t.Fatal("decoded-input defaults leaked into ordinary annotation AST", element.Column.TypeName)
		}
	}
}
