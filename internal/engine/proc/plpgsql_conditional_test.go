package proc

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func conditionalUnitDefinition(t *testing.T, source string) *Definition {
	t.Helper()
	integer := plUnitInteger(t)
	d := &Definition{Kind: PLpgSQLBody, Name: "conditional_unit", Result: integer.Usage(), Source: source}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d.PLSyntax = raw
	resolve := plUnitResolver(t, integer.Usage())
	d.PL, err = CompilePL(d, raw, false, plUnitCollation(t), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "record" {
			return sqltype.Usage{}, true, nil
		}
		return resolve(raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPLpgSQLConditionalCompiler(t *testing.T) {
	source := "DECLARE x integer := 7; BEGIN IF true THEN CASE x WHEN 7,8 THEN x := conditional_missing(); ELSE x := 9; END CASE; ELSE x := 10; END IF; RETURN x; END"
	d := conditionalUnitDefinition(t, source)
	untouched, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	body := d.PL.Root.Statements[0].Block
	branch := body.Statements[0].If.Arms[0].Statements[0].Case
	if d.PL.SearchCount != 1 || len(d.PL.Variables) != d.PL.FormalCount+len(body.Declarations)+1 || branch.SearchSlot != 0 || branch.Arms[0].Statements[0].Target.Slot != body.Bindings["x"] || !reflect.DeepEqual(untouched, d.PLSyntax) {
		t.Fatal("conditional compilation changed scopes/source or published a fake declaration")
	}
	found := d.PL.Variables[d.PL.FoundSlot]
	if d.PL.FoundSlot != d.PL.FormalCount || found.Name != "found" || found.Role != PLFoundSlot || !found.Usage.Valid() || found.Usage.Type.ID() != sqltype.Bool {
		t.Fatal("conditional compilation lost the canonical implicit FOUND")
	}
	if _, found := d.PL.Lookup([]string{PLSearchName(0)}, []*PLBlock{d.PL.Root, body}); found {
		t.Fatal("private search exposed as an ordinary local")
	}
	for _, source := range []string{
		"BEGIN IF false THEN missing := 1; END IF; RETURN 7; END",
		"DECLARE x CONSTANT integer := 7; BEGIN CASE WHEN false THEN x := 8; ELSE END CASE; RETURN x; END",
		"BEGIN IF false THEN RETURN; ELSE RETURN 7; END IF; END",
		"BEGIN IF false THEN DECLARE x integer; x integer; BEGIN END; END IF; RETURN 7; END",
	} {
		raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal("expected syntax-valid compile witness", err)
		}
		bad := &Definition{Name: "bad", Result: plUnitInteger(t).Usage(), Source: source}
		if _, err := CompilePL(bad, raw, false, plUnitCollation(t), plUnitResolver(t, plUnitInteger(t).Usage())); err == nil {
			t.Fatal("unreachable compile failure accepted", source)
		}
	}
	for _, tc := range []struct {
		name, source string
		names        []string
		variables    int
	}{
		{"postorder_nested", "BEGIN CASE 1 WHEN true THEN CASE 2 WHEN 2 THEN RETURN 7; ELSE RETURN 8; END CASE; ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_2__", "__Case__Variable_1__"}, 0},
		{"postorder_else_declaration", "BEGIN CASE 1 WHEN 1 THEN RETURN 7; ELSE DECLARE x integer; BEGIN CASE 2 WHEN 2 THEN RETURN 8; ELSE RETURN 9; END CASE; END; END CASE; END", []string{"__Case__Variable_3__", "__Case__Variable_2__"}, 1},
		{"postorder_width", "BEGIN CASE 1 WHEN true THEN DECLARE a1 integer; a2 integer; a3 integer; a4 integer; a5 integer; a6 integer; a7 integer; a8 integer; a9 integer; BEGIN RETURN 7; END; ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_10__"}, 9},
		{"recfield_repeated_alias", "DECLARE r record; a ALIAS FOR r; BEGIN CASE 1 WHEN true THEN RETURN r.f1+a.f1; ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_3__"}, 1},
		{"recfield_distinct", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.a+r.b; ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_4__"}, 1},
		{"recfield_qualified", "<<scope>> DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN scope.r.f1+r.f1; ELSE RETURN 9; END CASE; END scope", []string{"__Case__Variable_3__"}, 1},
		{"recfield_unreachable", "DECLARE r record; BEGIN CASE 1 WHEN true THEN IF false THEN RETURN r.absent; END IF; RETURN 7; ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_3__"}, 1},
		{"callee_record_field", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.absent(); ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_3__"}, 1},
		{"callee_column_same_identity", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.absent()+r.absent; ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_3__"}, 1},
		{"callee_decimal_width", "DECLARE r record; a1 integer; a2 integer; a3 integer; a4 integer; a5 integer; a6 integer; a7 integer; BEGIN CASE 1 WHEN true THEN RETURN r.absent(); ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_10__"}, 8},
		{"namespace_scalar_shadow", "DECLARE r record; BEGIN DECLARE r integer; BEGIN CASE 1 WHEN true THEN RETURN r.absent(); ELSE RETURN 9; END CASE; END; END", []string{"__Case__Variable_4__"}, 2},
		{"namespace_record_before_label", "<<s>> DECLARE r record; BEGIN DECLARE s record; BEGIN CASE 1 WHEN true THEN RETURN s.r.f+s.r; ELSE RETURN 9; END CASE; END; END s", []string{"__Case__Variable_4__"}, 2},
		{"recursive_target_fields", "DECLARE r record; s record; BEGIN CASE 1 WHEN true THEN r.a[s.idx] := r.b; ELSE RETURN 9; END CASE; END", []string{"__Case__Variable_6__"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := conditionalUnitDefinition(t, tc.source)
			var names []string
			var slots []int
			var walk func([]PLInstruction)
			walk = func(instructions []PLInstruction) {
				for _, instruction := range instructions {
					if instruction.Block != nil {
						walk(instruction.Block.Statements)
					}
					for _, c := range []*PLConditional{instruction.If, instruction.Case} {
						if c == nil {
							continue
						}
						if c.Search != nil {
							names = append(names, c.SearchName)
							slots = append(slots, c.SearchSlot)
						}
						for _, arm := range c.Arms {
							walk(arm.Statements)
						}
						walk(c.Else)
					}
				}
			}
			walk(d.PL.Root.Statements)
			if !slices.Equal(names, tc.names) || len(d.PL.Variables) != tc.variables+1 || d.PL.FoundSlot != d.PL.FormalCount || d.PL.Variables[d.PL.FoundSlot].Role != PLFoundSlot || d.PL.Variables[d.PL.FoundSlot].Usage.Type.ID() != sqltype.Bool || len(slots) != d.PL.SearchCount {
				t.Fatal("diagnostic/runtime datum namespace confusion", names, slots)
			}
			for i, slot := range slots {
				if slot != i {
					t.Fatal("runtime private indices ceased to be preorder", slots)
				}
			}
			original, err := parser.ParsePLpgSQL(tc.source, parser.PLReturnPolicy{})
			if err != nil || !reflect.DeepEqual(original, d.PLSyntax) {
				t.Fatal("diagnostic accounting mutated original syntax", err)
			}
		})
	}
	t.Run("composite_field_ledger", func(t *testing.T) {
		integer := plUnitInteger(t)
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "f1", Usage: integer.Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		const source = "BEGIN RETURN r.f1+a.f1+s.r.f1+r.absent()+r.absent; END"
		raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		program := &PLProgram{Variables: []PLVariable{{Name: "r", Usage: shape.Usage()}}}
		scope := &PLBlock{Label: "s", Bindings: map[string]int{"r": 0, "a": 0}}
		var ledger plDiagnosticDatums
		statement := raw.Statements[0]
		if err := ledger.expression(program, source, statement.ExpressionStart, statement.ExpressionEnd, []*PLBlock{scope}); err != nil {
			t.Fatal(err)
		}
		if ledger.extra != 2 || ledger.finishCase(1) != "__Case__Variable_4__" {
			t.Fatal("field existence, alias or qualification changed syntactic datum accounting")
		}
	})
	t.Run("recursive_target_ledger", func(t *testing.T) {
		program := &PLProgram{Variables: []PLVariable{{Name: "r", Record: true}, {Name: "s", Record: true}, {Name: "a", Usage: plUnitInteger(t).Usage()}}}
		scope := &PLBlock{Label: "scope", Bindings: map[string]int{"r": 0, "$1": 0, "s": 1, "a": 2}}
		for _, tc := range []struct {
			name, source string
			fields       []plDiagnosticField
		}{
			{"field_index", "BEGIN r.a[s.idx] := r.b; END", []plDiagnosticField{{0, "a"}, {1, "idx"}}},
			{"qualified_alias", "BEGIN scope.r.a[$1.a] := r.b; END", []plDiagnosticField{{0, "a"}}},
			{"parameter_target", "BEGIN $1.a[s.idx] := r.b; END", []plDiagnosticField{{0, "a"}, {1, "idx"}}},
			{"index_callee_dedup", "BEGIN r.a[s.idx()+s.idx] := r.b; END", []plDiagnosticField{{0, "a"}, {1, "idx"}}},
			{"quoted_field", `BEGIN "r"."a.b"[s.idx] := r.b; END`, []plDiagnosticField{{0, "a.b"}, {1, "idx"}}},
			{"literal_not_name", "BEGIN r.a['s.ignored'] := r.b; END", []plDiagnosticField{{0, "a"}}},
			{"post_subscript_field", "BEGIN a[1].f := r.b; END", nil},
			{"not_assignment", "BEGIN RETURN r.b; END", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				raw, err := parser.ParsePLpgSQL(tc.source, parser.PLReturnPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				var ledger plDiagnosticDatums
				if err := ledger.target(program, tc.source, &raw.Statements[0], []*PLBlock{scope}); err != nil {
					t.Fatal(err)
				}
				if ledger.extra != len(tc.fields) || len(ledger.fields) != len(tc.fields) {
					t.Fatal("target/RHS boundary or recursive target accounting changed", ledger.fields)
				}
				for _, key := range tc.fields {
					if _, found := ledger.fields[key]; !found {
						t.Fatal("target field identity missing", key)
					}
				}
			})
		}
	})
	t.Run("diagnostic_parent_namespace", func(t *testing.T) {
		integer := plUnitInteger(t)
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "f", Usage: integer.Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		program := &PLProgram{Variables: []PLVariable{
			{Name: "r", Record: true}, {Name: "v", Usage: integer.Usage()},
			{Name: "s", Record: true}, {Name: "c", Usage: shape.Usage()},
		}}
		scope := func(label string, bindings map[string]int) *PLBlock {
			return &PLBlock{Label: label, Bindings: bindings}
		}
		outer := scope("s", map[string]int{"r": 0, "a": 0})
		innerScalar := scope("s", map[string]int{"r": 1})
		innerRecord := scope("inner", map[string]int{"s": 2})
		for _, tc := range []struct {
			name, path string
			scopes     []*PLBlock
			slot, used int
			found      bool
		}{
			{"scalar_shadow", "r.f", []*PLBlock{outer, innerScalar}, 0, 1, true},
			{"record_before_outer_label", "s.r.f", []*PLBlock{outer, innerRecord}, 2, 1, true},
			{"qualified_scalar_two_stops", "s.r", []*PLBlock{outer, innerScalar}, 1, 2, true},
			{"qualified_scalar_three_skips", "s.r.f", []*PLBlock{outer, innerScalar}, 0, 2, true},
			{"record_before_same_label", "s.r.f", []*PLBlock{scope("s", map[string]int{"s": 2, "r": 0})}, 2, 1, true},
			{"scalar_then_same_label", "s.r.f", []*PLBlock{scope("s", map[string]int{"s": 1, "r": 0})}, 0, 2, true},
			{"qualified_record_two_stops", "s.r", []*PLBlock{scope("", map[string]int{"s": 0}), scope("s", map[string]int{"r": 2})}, 2, 2, true},
			{"shadowed_label_missing_binding", "s.r.f", []*PLBlock{outer, scope("s", map[string]int{"x": 1})}, 0, 2, true},
			{"alias_scalar_shadow", "a.f", []*PLBlock{outer, scope("", map[string]int{"a": 1})}, 0, 1, true},
			{"composite_unqualified", "c.f", []*PLBlock{scope("", map[string]int{"c": 3})}, 3, 1, true},
			{"formal_function_label", "conditional_unit.arg.f", []*PLBlock{scope("conditional_unit", map[string]int{"arg": 3, "$1": 3})}, 3, 2, true},
			{"formal_parameter_alias", "$1.f", []*PLBlock{scope("conditional_unit", map[string]int{"arg": 3, "$1": 3})}, 3, 1, true},
			{"initializer_previous_visibility", "r.f", []*PLBlock{outer, scope("inner", map[string]int{"earlier": 1})}, 0, 1, true},
			{"scalar_single_component", "r", []*PLBlock{outer, innerScalar}, 1, 1, true},
			{"no_eligible_scalar", "r.f", []*PLBlock{innerScalar}, 0, 0, false},
			{"no_scopes", "r.f", nil, 0, 0, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var parts []parser.Identifier
				for _, text := range strings.Split(tc.path, ".") {
					parts = append(parts, parser.Identifier{Text: text})
				}
				slot, used, found, err := plDiagnosticParent(program, parts, tc.scopes)
				if err != nil || slot != tc.slot || used != tc.used || found != tc.found {
					t.Fatal("diagnostic namespace mismatch", slot, used, found, err)
				}
				var ledger plDiagnosticDatums
				if err := ledger.name(program, parts, tc.scopes); err != nil {
					t.Fatal(err)
				}
				count := 0
				if tc.found && tc.used < len(parts) {
					count = 1
					if _, exists := ledger.fields[plDiagnosticField{parent: tc.slot, name: parts[tc.used].Text}]; !exists {
						t.Fatal("field identity ignored names_used or actual parent slot")
					}
				}
				if ledger.extra != count || len(ledger.fields) != count {
					t.Fatal("whole-variable match failed to stop field accounting", ledger.extra)
				}
			})
		}
		for _, tc := range []struct {
			path  string
			scope *PLBlock
		}{
			{"r.f", scope("", map[string]int{"r": -1})},
			{"s.r", scope("s", map[string]int{"r": len(program.Variables)})},
		} {
			var parts []parser.Identifier
			for _, text := range strings.Split(tc.path, ".") {
				parts = append(parts, parser.Identifier{Text: text})
			}
			if _, _, _, err := plDiagnosticParent(program, parts, []*PLBlock{tc.scope}); err == nil {
				t.Fatal("invalid bound slot admitted", tc.path)
			}
		}
		var ledger plDiagnosticDatums
		const source = "r.f()+a.f+s.r.f"
		if err := ledger.expression(program, source, parser.Position{Character: 1}, parser.Position{Offset: len(source), Character: len(source) + 1}, []*PLBlock{outer, scope("", map[string]int{"a": 1})}); err != nil {
			t.Fatal(err)
		}
		if ledger.extra != 1 || len(ledger.fields) != 1 {
			t.Fatal("eligible alias and qualified parent did not share field identity")
		}
	})
}

func TestPLpgSQLConditionalValidation(t *testing.T) {
	d := conditionalUnitDefinition(t, "DECLARE x integer := 7; y integer := 8; BEGIN CASE x WHEN 7,8 THEN x := 9; WHEN 10 THEN x := 11; ELSE x := 12; END CASE; RETURN x; END")
	for _, tc := range []struct {
		name    string
		corrupt func(*PLProgram, *PLConditional)
	}{
		{"missing_search", func(_ *PLProgram, c *PLConditional) { c.Search = nil }},
		{"foreign_search", func(_ *PLProgram, c *PLConditional) { c.Search = c.Arms[0].Query }},
		{"missing_arm", func(_ *PLProgram, c *PLConditional) { c.Arms = c.Arms[:1] }},
		{"reordered_arms", func(_ *PLProgram, c *PLConditional) { c.Arms[0], c.Arms[1] = c.Arms[1], c.Arms[0] }},
		{"missing_query", func(_ *PLProgram, c *PLConditional) { c.Arms[0].Query = nil }},
		{"foreign_query", func(_ *PLProgram, c *PLConditional) { c.Arms[0].Query = c.Search }},
		{"extra_search_slot", func(p *PLProgram, _ *PLConditional) { p.SearchCount++ }},
		{"cross_owned_slot", func(_ *PLProgram, c *PLConditional) { c.SearchSlot++ }},
		{"private_name", func(_ *PLProgram, c *PLConditional) { c.SearchName = "user_visible" }},
		{"missing_else", func(_ *PLProgram, c *PLConditional) { c.Else = nil }},
		{"wrong_else_presence", func(_ *PLProgram, c *PLConditional) { c.HasElse = !c.HasElse }},
		{"operator_origin", func(_ *PLProgram, c *PLConditional) { c.Arms[0].OperatorPosition.Character-- }},
		{"rendered_origin", func(_ *PLProgram, c *PLConditional) { c.Arms[0].DiagnosticItems++ }},
		{"rendered_query", func(_ *PLProgram, c *PLConditional) { c.Arms[0].DiagnosticQuery += "garbage" }},
		{"redirected_target", func(p *PLProgram, c *PLConditional) {
			c.Arms[0].Statements = slices.Clone(c.Arms[0].Statements)
			other, ok := p.Root.Statements[0].Block.Bindings["y"]
			if !ok || other == c.Arms[0].Statements[0].Target.Slot {
				t.Fatal("redirect witness does not name a distinct declared target")
			}
			c.Arms[0].Statements[0].Target.Slot = other
		}},
		{"foreign_items", func(_ *PLProgram, c *PLConditional) {
			q := *c.Arms[0].Query
			in := q.Targets[0].(parser.InListExpr)
			in.Items = slices.Clone(in.Items)
			in.Items[0] = in.Items[1]
			q.Targets = []parser.Expr{in}
			c.Arms[0].Query = &q
		}},
		{"extra_query_clause", func(_ *PLProgram, c *PLConditional) {
			q := *c.Arms[0].Query
			q.Where = c.Search.Targets[0]
			c.Arms[0].Query = &q
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := *d.PL
			root := *program.Root
			root.Statements = slices.Clone(root.Statements)
			body := *root.Statements[0].Block
			body.Statements = slices.Clone(body.Statements)
			conditional := *body.Statements[0].Case
			conditional.Arms = slices.Clone(conditional.Arms)
			body.Statements[0].Case = &conditional
			root.Statements[0].Block = &body
			program.Root = &root
			tc.corrupt(&program, &conditional)
			broken := *d
			broken.PL = &program
			if err := program.ValidateDefinition(&broken); err == nil {
				t.Fatal("forged conditional accepted")
			}
			if err := d.PL.ValidateDefinition(d); err != nil {
				t.Fatal("forged copy mutated published source", err)
			}
		})
	}
	for _, name := range []string{"if_search", "if_foreign_query", "if_extra_else"} {
		t.Run(name, func(t *testing.T) {
			original := conditionalUnitDefinition(t, "BEGIN IF true THEN RETURN 7; END IF; RETURN 8; END")
			program := *original.PL
			root := *program.Root
			root.Statements = slices.Clone(root.Statements)
			body := *root.Statements[0].Block
			body.Statements = slices.Clone(body.Statements)
			conditional := *body.Statements[0].If
			conditional.Arms = slices.Clone(conditional.Arms)
			switch name {
			case "if_search":
				conditional.Search = body.Statements[1].Query
			case "if_foreign_query":
				conditional.Arms[0].Query = body.Statements[1].Query
			case "if_extra_else":
				conditional.Else = []PLInstruction{body.Statements[1]}
			}
			body.Statements[0].If = &conditional
			root.Statements[0].Block = &body
			program.Root = &root
			broken := *original
			broken.PL = &program
			if err := program.ValidateDefinition(&broken); err == nil {
				t.Fatal("forged IF accepted")
			}
			if err := original.PL.ValidateDefinition(original); err != nil {
				t.Fatal("forged IF mutated original", err)
			}
		})
	}
	t.Run("nested_cross_owned_search", func(t *testing.T) {
		original := conditionalUnitDefinition(t, "BEGIN CASE 1 WHEN 1 THEN CASE 2 WHEN 2 THEN RETURN 7; ELSE RETURN 8; END CASE; ELSE RETURN 9; END CASE; END")
		program := *original.PL
		root := *program.Root
		root.Statements = slices.Clone(root.Statements)
		body := *root.Statements[0].Block
		body.Statements = slices.Clone(body.Statements)
		outer := *body.Statements[0].Case
		outer.Arms = slices.Clone(outer.Arms)
		outer.Arms[0].Statements = slices.Clone(outer.Arms[0].Statements)
		inner := *outer.Arms[0].Statements[0].Case
		inner.SearchSlot = outer.SearchSlot
		outer.Arms[0].Statements[0].Case = &inner
		body.Statements[0].Case = &outer
		root.Statements[0].Block = &body
		program.Root = &root
		broken := *original
		broken.PL = &program
		if err := program.ValidateDefinition(&broken); err == nil {
			t.Fatal("in-range cross-owned search slot accepted")
		}
		if err := original.PL.ValidateDefinition(original); err != nil {
			t.Fatal("forged nested CASE mutated original", err)
		}
	})
	for _, tc := range []struct {
		name, source, forged string
		nested               bool
	}{
		{"forged_preorder_name", "BEGIN CASE 1 WHEN true THEN CASE 2 WHEN 2 THEN RETURN 7; ELSE RETURN 8; END CASE; ELSE RETURN 9; END CASE; END", "__Case__Variable_1__", false},
		{"forged_omitted_callee_name", "DECLARE r record; BEGIN CASE 1 WHEN true THEN RETURN r.absent(); ELSE RETURN 9; END CASE; END", "__Case__Variable_2__", false},
		{"forged_scalar_shadow_name", "DECLARE r record; BEGIN DECLARE r integer; BEGIN CASE 1 WHEN true THEN RETURN r.absent(); ELSE RETURN 9; END CASE; END; END", "__Case__Variable_3__", true},
		{"forged_record_before_label_name", "<<s>> DECLARE r record; BEGIN DECLARE s record; BEGIN CASE 1 WHEN true THEN RETURN s.r.f+s.r; ELSE RETURN 9; END CASE; END; END s", "__Case__Variable_5__", true},
		{"forged_omitted_target_fields", "DECLARE r record; s record; BEGIN CASE 1 WHEN true THEN r.a[s.idx] := r.b; ELSE RETURN 9; END CASE; END", "__Case__Variable_4__", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := conditionalUnitDefinition(t, tc.source)
			program := *original.PL
			root := *program.Root
			root.Statements = slices.Clone(root.Statements)
			body := *root.Statements[0].Block
			body.Statements = slices.Clone(body.Statements)
			container := &body
			if tc.nested {
				nested := *body.Statements[0].Block
				nested.Statements = slices.Clone(nested.Statements)
				body.Statements[0].Block = &nested
				container = &nested
			}
			outer := *container.Statements[0].Case
			outer.Arms = slices.Clone(outer.Arms)
			old := outer.SearchName
			outer.SearchName = tc.forged
			for i := range outer.Arms {
				outer.Arms[i].DiagnosticQuery = strings.ReplaceAll(outer.Arms[i].DiagnosticQuery, old, outer.SearchName)
			}
			container.Statements[0].Case = &outer
			root.Statements[0].Block = &body
			program.Root = &root
			broken := *original
			broken.PL = &program
			if err := program.ValidateDefinition(&broken); err == nil {
				t.Fatal("consistently rendered but incorrectly accounted diagnostic name accepted")
			}
			if err := original.PL.ValidateDefinition(original); err != nil {
				t.Fatal("forgery changed original", err)
			}
		})
	}
	t.Run("initializer_delimiter_forgery", func(t *testing.T) {
		original := conditionalUnitDefinition(t, "DECLARE x integer := 7 /* trailing */; BEGIN RETURN x; END")
		program := *original.PL
		root := *program.Root
		root.Statements = slices.Clone(root.Statements)
		body := *root.Statements[0].Block
		body.Declarations = slices.Clone(body.Declarations)
		body.Declarations[0].End = body.Declarations[0].Delimiter
		root.Statements[0].Block = &body
		program.Root = &root
		broken := *original
		broken.PL = &program
		if err := program.ValidateDefinition(&broken); err == nil {
			t.Fatal("delimiter substituted for content end")
		}
		if err := original.PL.ValidateDefinition(original); err != nil {
			t.Fatal("span forgery changed original", err)
		}
	})
}

func TestPLpgSQLConditionalFrame(t *testing.T) {
	d := conditionalUnitDefinition(t, "BEGIN CASE 1 WHEN 1 THEN CASE 2 WHEN 2 THEN RETURN 7; ELSE RETURN 8; END CASE; ELSE RETURN 9; END CASE; END")
	if d.PL.SearchCount != 2 || d.PL.FoundSlot != d.PL.FormalCount || d.PL.FoundSlot < 0 || d.PL.FoundSlot >= len(d.PL.Variables) {
		t.Fatal("private search layout or canonical FOUND owner")
	}
	for slot, variable := range d.PL.Variables {
		if slot != d.PL.FoundSlot || variable.Role != PLFoundSlot || !variable.Usage.Valid() || variable.Usage.Type.ID() != sqltype.Bool {
			t.Fatal("private CASE search became an executable declaration")
		}
	}
	input := NewFrame(nil)
	defer input.Close()
	first := NewPLFrame(d.PL, d, input, sqltype.CollationRef{})
	defer first.Close()
	second := NewPLFrame(d.PL, d, input, sqltype.CollationRef{})
	defer second.Close()
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.VarChar {
			text = typ
		}
	}
	if text == nil {
		t.Fatal("missing varchar")
	}
	usage := text.Usage()
	usage.Typmod = 9
	var err error
	usage.Collation, err = sqltype.RegisteredCollation(6411, 6411)
	if err != nil {
		t.Fatal(err)
	}
	first.Searches[0] = PLSearch{Value: sqltype.Null(text), Usage: usage}
	first.Searches[1] = PLSearch{Value: sqltype.String(text, "once"), Usage: usage}
	captured := first.Searches[1]
	if !first.Searches[0].Value.IsNull() || first.Searches[0].Value.Type() != text || first.Searches[0].Usage != usage || second.Searches[0].Value.Type() != nil {
		t.Fatal("typed NULL/full Usage/invocation isolation")
	}
	for _, name := range []string{"x", "$1", "case:0", "\x00case:-1", "\x00case:00", "\x00case:2", "\x00case:0suffix"} {
		if _, found := d.PL.SearchSlot(name); found {
			t.Fatal("noncanonical private search name accepted", name)
		}
	}
	if index, found := d.PL.SearchSlot(PLSearchName(1)); !found || index != 1 {
		t.Fatal("canonical search missing")
	}
	first.Close()
	first.Close()
	if first.Searches != nil || second.Searches[1].Value.Type() != nil || captured.Value != sqltype.String(text, "once") || captured.Usage != usage || d.PL.SearchCount != 2 {
		t.Fatal("frame close damaged detached value or shared program")
	}
}
