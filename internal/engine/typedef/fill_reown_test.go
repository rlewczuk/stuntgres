package typedef

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestReownAddedColumnFill(t *testing.T) {
	ctx := context.Background()
	mood, err := sqltype.Enum(45000, "mood", "public.mood", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	nextGeneration, err := sqltype.EnumWithMembers(45000, "mood", "public.mood", []sqltype.EnumMember{
		{ID: 1, Label: "a", SortOrder: 1},
		{ID: 3, Label: "b", SortOrder: 2},
		{ID: 5, Label: "c", SortOrder: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := sqltype.Enum(45001, "mood", "public.mood", []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	foreignReplacement, err := sqltype.Enum(45002, "mood", "public.mood", []string{"b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	moodArray, err := sqltype.Array(45010, "_mood", mood)
	if err != nil {
		t.Fatal(err)
	}
	replacementArray, err := sqltype.Array(45011, "_mood", replacement)
	if err != nil {
		t.Fatal(err)
	}
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			text = d
			break
		}
	}
	if text == nil {
		t.Fatal("missing builtin text descriptor")
	}
	textDomain, err := sqltype.Domain(45020, "mood_text", "public.mood_text", text.Usage())
	if err != nil {
		t.Fatal(err)
	}
	moodValue := func(label string) sqltype.Value {
		value, err := mood.Input(ctx, label, -1)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	arrayValue, err := sqltype.ArrayValue(moodArray, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{moodValue("a")})
	if err != nil {
		t.Fatal(err)
	}

	relation := func(target *sqltype.Descriptor) *catalog.Relation {
		return &catalog.Relation{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 50000},
			ObjectName:    "fill_relation",
			Attributes:    []int32{1},
			Columns:       []catalog.Column{{Name: "x", Usage: sqltype.Usage{Type: target, Typmod: -1}}},
		}
	}
	state := func(target *sqltype.Descriptor) *catalog.State {
		next := catalog.NewState(1)
		next.SetDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.ID())}, &catalog.Type{SQLType: target})
		return next
	}

	tests := []struct {
		name      string
		next      *catalog.State
		relation  *catalog.Relation
		attribute int32
		value     sqltype.Value
		present   bool
		wantErr   bool
		wantType  *sqltype.Descriptor
		wantNull  bool
		wantText  string
		wantLen   int
	}{
		{name: "absent relation", next: state(mood), relation: nil, attribute: 1, value: sqltype.Null(mood), present: false},
		{name: "absent attribute", next: state(mood), relation: relation(mood), attribute: 2, value: sqltype.Null(mood), present: false},
		{name: "nil declared type", next: state(mood), relation: relation(nil), attribute: 1, value: sqltype.Null(mood), present: false},
		{name: "pointer equal", next: state(mood), relation: relation(mood), attribute: 1, value: sqltype.Null(mood), present: true, wantType: mood, wantNull: true},
		{name: "same identity null", next: state(nextGeneration), relation: relation(nextGeneration), attribute: 1, value: sqltype.Null(mood), present: true, wantType: nextGeneration, wantNull: true},
		{name: "same identity label", next: state(nextGeneration), relation: relation(nextGeneration), attribute: 1, value: moodValue("b"), present: true, wantType: nextGeneration, wantText: "b"},
		{name: "replacement null", next: state(replacement), relation: relation(replacement), attribute: 1, value: sqltype.Null(mood), present: true, wantType: replacement, wantNull: true},
		{name: "replacement label", next: state(replacement), relation: relation(replacement), attribute: 1, value: moodValue("a"), present: true, wantType: replacement, wantText: "a"},
		{name: "replacement missing label", next: state(foreignReplacement), relation: relation(foreignReplacement), attribute: 1, value: moodValue("a"), present: false, wantErr: true},
		{name: "replacement array", next: state(replacementArray), relation: relation(replacementArray), attribute: 1, value: arrayValue, present: true, wantType: replacementArray, wantLen: 1},
		{name: "replacement array null", next: state(replacementArray), relation: relation(replacementArray), attribute: 1, value: sqltype.Null(moodArray), present: true, wantType: replacementArray, wantNull: true},
		{name: "enum to text non-null", next: state(text), relation: relation(text), attribute: 1, value: moodValue("a"), present: false, wantErr: true},
		{name: "enum to text null fallback", next: state(text), relation: relation(text), attribute: 1, value: sqltype.Null(mood), present: true, wantType: text, wantNull: true},
		{name: "domain target null", next: state(textDomain), relation: relation(textDomain), attribute: 1, value: sqltype.Null(mood), present: false, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reowned, present, err := ReownAddedColumnFill(test.next, test.relation, test.attribute, test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %v", reowned)
				}
				if present {
					t.Fatal("errored fill still reported present")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if present != test.present {
				t.Fatalf("present = %t, want %t", present, test.present)
			}
			if !present {
				return
			}
			if reowned.Type() != test.wantType {
				t.Fatalf("type = %v, want %v", reowned.Type(), test.wantType)
			}
			if reowned.IsNull() != test.wantNull {
				t.Fatalf("null = %t, want %t", reowned.IsNull(), test.wantNull)
			}
			if test.wantText != "" {
				label, err := reowned.Type().Output(ctx, reowned)
				if err != nil {
					t.Fatal(err)
				}
				if label != test.wantText {
					t.Fatalf("label = %q, want %q", label, test.wantText)
				}
			}
			if test.wantLen != 0 && reowned.ArrayLen() != test.wantLen {
				t.Fatalf("array length = %d, want %d", reowned.ArrayLen(), test.wantLen)
			}
		})
	}

	imports := state(replacement)
	imports.SetScalarImport(text, text)
	if _, present, err := ReownAddedColumnFill(imports, relation(replacement), 1, moodValue("a")); err != nil || !present {
		t.Fatalf("replacement with existing imports: present = %t, err = %v", present, err)
	}
	if got := imports.ScalarImports(); len(got) != 1 || got[text] != text {
		t.Fatalf("catalog scalar imports mutated: %v", got)
	}
}