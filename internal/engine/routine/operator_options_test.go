package routine

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func operatorOptionDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var actual *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &actual) || !reflect.DeepEqual(actual, want) {
		t.Fatalf("diagnostic = %#v (%v), want %#v", actual, err, want)
	}
}

func operatorParsedOptions(t *testing.T, text string) []parser.OperatorOption {
	t.Helper()
	statements, err := parser.Parse("CREATE OPERATOR public.#%# (" + text + ")")
	if err != nil {
		t.Fatal(text, err)
	}
	return statements[0].(*parser.CreateOperator).Options
}

func TestUserOperatorOptionExtraction(t *testing.T) {
	for _, literal := range []string{"", "int4", "integer", "pg_catalog.int4", `A."B"`, "int4[]", "SETOF int4", "numeric(8,2)", " Int4 "} {
		pos := parser.Position{Offset: 27, Character: 28}
		o := parser.OperatorOption{Name: "function", HasValue: true, Kind: parser.OperatorOptionStringArgument, Value: literal, ValuePos: pos}
		name, err := OperatorOptionName(o)
		if err != nil || !reflect.DeepEqual(name.Parts, []parser.Identifier{{Text: literal, Pos: pos}}) {
			t.Fatal("literal name reinterpreted", literal, name, err)
		}
		o.Name = "leftarg"
		typ, err := OperatorOptionType(o)
		want := parser.TypeName{Name: literal, Quoted: true, Parts: []parser.Identifier{{Text: literal, Pos: pos}}, Pos: pos}
		if err != nil || !reflect.DeepEqual(typ, want) {
			t.Fatal("literal type reinterpreted", literal, typ, err)
		}
	}
	for _, text := range []string{"function=int4", "function=int4[]", "function=int4(8)", "function=SETOF int4"} {
		o := operatorParsedOptions(t, text)[0]
		name, err := OperatorOptionName(o)
		if err != nil || len(name.Parts) != 1 || name.Parts[0].Text != "int4" {
			t.Fatal("generic name canonicalized as a type", text, name, err)
		}
	}
	for _, tc := range []struct {
		text  string
		parts []string
	}{
		{"INTEGER", []string{"pg_catalog", "int4"}},
		{"BOOLEAN", []string{"pg_catalog", "bool"}},
		{"NUMERIC", []string{"pg_catalog", "numeric"}},
		{`"integer"`, []string{"integer"}},
		{"pg_catalog.int4", []string{"pg_catalog", "int4"}},
	} {
		o := operatorParsedOptions(t, "function="+tc.text)[0]
		name, err := OperatorOptionName(o)
		if err != nil || len(name.Parts) != len(tc.parts) {
			t.Fatal(tc.text, name, err)
		}
		for i, part := range tc.parts {
			if name.Parts[i].Text != part {
				t.Fatal("SQL alias/generic name provenance", tc.text, name)
			}
		}
	}
	for _, tc := range []struct{ text, nameError, typeError string }{
		{"function", "function requires a parameter", "function requires a parameter"},
		{"function=7", "argument of function must be a name", "argument of function must be a type name"},
		{"function=1.0", "argument of function must be a name", "argument of function must be a type name"},
		{"function=OPERATOR(public.+)", "", "argument of function must be a type name"},
	} {
		o := operatorParsedOptions(t, tc.text)[0]
		_, err := OperatorOptionName(o)
		if tc.nameError != "" {
			operatorOptionDiagnostic(t, err, "42601", tc.nameError, "")
		} else if err != nil {
			t.Fatal(err)
		}
		_, err = OperatorOptionType(o)
		operatorOptionDiagnostic(t, err, "42601", tc.typeError, "")
	}
	for _, text := range []string{"function=pg_catalog.int4[]", "function=pg_catalog.numeric(8,2)", "function=SETOF pg_catalog.int4"} {
		o := operatorParsedOptions(t, text)[0]
		name, err := OperatorOptionName(o)
		if err != nil || len(name.Parts) != 2 || name.Parts[0].Text != "pg_catalog" || name.Parts[1].Text != o.Type.Name {
			t.Fatal("name extraction must ignore type decorations only", text, name, err)
		}
		typ, err := OperatorOptionType(o)
		if err != nil || !reflect.DeepEqual(typ, o.Type) {
			t.Fatal("type extraction lost decorations", text, typ, err)
		}
	}
	for _, tc := range []struct {
		rhs   string
		want  bool
		valid bool
	}{
		{"", true, true}, {"true", true, true}, {"'TrUe'", true, true}, {"ON", true, true}, {"'ON'", true, true}, {"false", false, true}, {"off", false, true}, {"'OFF'", false, true},
		{"0", false, true}, {"1", true, true}, {"+1", true, true}, {"-0", false, true}, {"+0", false, true}, {"0x1", true, true}, {"0o0", false, true}, {"0b1", true, true}, {"0_1", true, true},
		{"-1", false, false}, {"2", false, false}, {"1.0", false, false}, {"1e0", false, false}, {"'1'", false, false}, {"'0'", false, false}, {"'t'", false, false}, {"'tru'", false, false}, {"'of'", false, false}, {"'o'", false, false}, {"'yes'", false, false}, {"'no'", false, false}, {"''", false, false}, {"' true'", false, false}, {"pg_catalog.off", false, false}, {"off[]", false, false}, {"off(1)", false, false}, {"SETOF off", false, false}, {"NONE", false, false},
	} {
		text := "hashes"
		if tc.rhs != "" {
			text += "=" + tc.rhs
		}
		o := operatorParsedOptions(t, text)[0]
		got, err := OperatorBoolean(o)
		if tc.valid {
			if err != nil || got != tc.want {
				t.Fatal(text, got, err)
			}
		} else {
			operatorOptionDiagnostic(t, err, "42601", "hashes requires a Boolean value", "")
		}
	}
	for _, text := range []string{"restrict", "restrict=NONE", "restrict='none'", `restrict="none"`} {
		o := operatorParsedOptions(t, text)[0]
		create := o
		o = OperatorAlterOption(o)
		nullable := text == "restrict" || text == "restrict=NONE"
		if o.HasValue == nullable {
			t.Fatal("ALTER NULL classification", text, o)
		}
		if !nullable {
			name, err := OperatorOptionName(o)
			if err != nil || len(name.Parts) != 1 || name.Parts[0].Text != "none" {
				t.Fatal(text, name, err)
			}
		}
		if text == "restrict=NONE" {
			name, err := OperatorOptionName(create)
			if err != nil || name.Parts[0].Text != "none" {
				t.Fatal("CREATE NONE lost real name", name, err)
			}
		}
	}
	for _, tc := range []struct {
		text    string
		alter   bool
		message string
	}{
		{"function=7,function=int4pl", false, "argument of function must be a name"},
		{"leftarg=7,leftarg=int4", false, "argument of leftarg must be a type name"},
		{"rightarg=SETOF int4,rightarg=int4", false, "SETOF type not allowed for operator argument"},
		{"hashes='t',hashes=true", false, "hashes requires a Boolean value"},
		{"function=7", true, "argument of function must be a name"},
		{"userop_unknown=7", true, "argument of userop_unknown must be a name"},
		{"sort1=7", true, "argument of sort1 must be a name"},
		{"hashes=1", true, "argument of hashes must be a name"},
		{"commutator=NONE", true, "commutator requires a parameter"},
		{"negator", true, "negator requires a parameter"},
		{"function=NONE", true, `operator attribute "function" cannot be changed`},
	} {
		code := "42601"
		if tc.message == "SETOF type not allowed for operator argument" {
			code = "42P13"
		}
		operatorOptionDiagnostic(t, operatorOptionsSyntax(nil, operatorParsedOptions(t, tc.text), tc.alter), code, tc.message, "")
	}
	for _, text := range []string{"commutator='abc',commutator='#=#'", "leftarg=missing_type,leftarg=int4", "function=missing_function,function=int4pl", "sort1=7,merges=false", "sort2", "hashes=true,hashes=false"} {
		if err := operatorOptionsSyntax(nil, operatorParsedOptions(t, text), false); err != nil {
			t.Fatal("premature semantic lookup/obsolete extraction", text, err)
		}
	}
	for _, text := range []string{"restrict,join=NONE,hashes=NONE,merges", "restrict='none'", `join="none"`} {
		if err := operatorOptionsSyntax(nil, operatorParsedOptions(t, text), true); err != nil {
			t.Fatal("ALTER nullable/name extraction", text, err)
		}
	}
}
