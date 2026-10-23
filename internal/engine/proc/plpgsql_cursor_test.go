package proc

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func cursorProgramUnit(t *testing.T) (*Definition, *PLProgram, map[sqltype.ID]*sqltype.Descriptor) {
	t.Helper()
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	d := &Definition{Kind: PLpgSQLBody, Name: "cursor_unit", Source: "DECLARE before_value integer:=10; c SCROLL CURSOR(a integer,b text) FOR SELECT before_value+a AS i,b AS t; later_value integer:=20; n integer:=0; BEGIN OPEN c(b=>'x',a=>7); FETCH c INTO n; FOR r IN c(1,'t') LOOP n:=n+r.i; END LOOP; CLOSE c; RETURN n; END", Result: types[sqltype.Int4].Usage()}
	if err := d.SetFormals([]Formal{{Mode: "i", Name: "input", Usage: types[sqltype.Int4].Usage()}}); err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(d.Source, PLReturnPolicy(d.Result, len(d.OutputFormals), false))
	if err != nil {
		t.Fatal(err)
	}
	p, err := CompilePL(d, raw, false, plUnitCollation(t), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		id := sqltype.ID(0)
		switch raw.Type.Name {
		case "boolean", "bool":
			id = sqltype.Bool
		case "integer", "int4":
			id = sqltype.Int4
		case "text":
			id = sqltype.Text
		case "refcursor":
			id = sqltype.Refcursor
		default:
			return sqltype.Usage{}, false, fmt.Errorf("unexpected type %s", raw.Type.Name)
		}
		return types[id].Usage(), false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d.PLSyntax, d.PL = raw, p
	return d, p, types
}

func TestPLpgSQLCursorCompile(t *testing.T) {
	d, p, types := cursorProgramUnit(t)
	if err := p.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	body := p.Root.Statements[0].Block
	c := body.Bindings["c"]
	recipe := p.Variables[c].Cursor
	if p.FormalCount != 1 || p.Root.Bindings["$1"] != 0 || p.FoundSlot != 1 {
		t.Fatal("formal/FOUND layout")
	}
	if slot, ok := p.Lookup([]string{"found"}, []*PLBlock{p.Root}); !ok || slot != p.FoundSlot {
		t.Fatal("implicit FOUND overlay")
	}
	if _, canonical := p.Root.Bindings["found"]; canonical {
		t.Fatal("implicit FOUND corrupted canonical formal map")
	}
	if len(recipe.Arguments) != 2 || p.Variables[recipe.Arguments[0]].Name != "a" || p.Variables[recipe.Arguments[1]].Name != "b" {
		t.Fatal("argument order")
	}
	if len(recipe.Scopes) != 3 || recipe.Scopes[1].Bindings["before_value"] != body.Bindings["before_value"] {
		t.Fatal("declaration lexical snapshot")
	}
	if _, leaked := recipe.Scopes[1].Bindings["later_value"]; leaked {
		t.Fatal("later declaration captured")
	}
	if recipe.Scopes[2].Label != "c" || recipe.Scopes[2].Bindings["a"] != recipe.Arguments[0] {
		t.Fatal("qualified argument namespace")
	}
	open := body.Statements[0].Cursor
	if open.Arguments[0].Slot != recipe.Arguments[0] || open.Arguments[0].Expression.SourcePosition().Offset < open.Arguments[1].Expression.SourcePosition().Offset {
		t.Fatal("named actuals not mapped before evaluation")
	}
	loop := body.Statements[2].Cursor
	if p.Variables[loop.LoopSlot].Role != PLLoopRecordSlot || !p.Variables[loop.LoopSlot].Record || loop.Loop.Bindings["r"] != loop.LoopSlot {
		t.Fatal("implicit loop RECORD")
	}
	if _, leaked := body.Bindings["r"]; leaked {
		t.Fatal("loop binding escaped")
	}
	input := NewFrame(d.Inputs)
	defer input.Close()
	seven, _ := sqltype.Integer(types[sqltype.Int4], 7)
	if err := input.Bind(0, seven, types[sqltype.Int4].Usage()); err != nil {
		t.Fatal(err)
	}
	one := NewPLFrame(p, d, input, sqltype.CollationRef{})
	defer one.Close()
	two := NewPLFrame(p, d, input, sqltype.CollationRef{})
	defer two.Close()
	one.SetFound(true)
	first, _ := one.Values[p.FoundSlot].Boolean()
	second, _ := two.Values[p.FoundSlot].Boolean()
	if !first || second || input.Parameters[0] != seven || two.Values[0] != seven {
		t.Fatal("invocation values/FOUND leaked")
	}
	for _, source := range []string{"BEGIN CLOSE c; END trailing", "DECLARE c refcursor; BEGIN OPEN c FOR SELECT 7; END junk"} {
		if _, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{}); err == nil {
			t.Fatal("valid prefix accepted", source)
		}
	}
}

func TestPLpgSQLCursorValidation(t *testing.T) {
	cases := map[string]func(*PLProgram, *PLBlock, int){
		"missing_found":          func(p *PLProgram, b *PLBlock, c int) { p.FoundSlot = -1 },
		"redirect_found":         func(p *PLProgram, b *PLBlock, c int) { p.Root.Bindings["found"] = 0 },
		"extra_formal_namespace": func(p *PLProgram, b *PLBlock, c int) { p.Root.Bindings["foreign"] = p.FoundSlot },
		"forged_function_scope":  func(p *PLProgram, b *PLBlock, c int) { b.FunctionScope = true },
		"hidden_function_scope":  func(p *PLProgram, b *PLBlock, c int) { p.Root.FunctionScope = false },
		"cursor_scope_role":      func(p *PLProgram, b *PLBlock, c int) { p.Variables[c].Cursor.Scopes[0].FunctionScope = false },
		"argument_role": func(p *PLProgram, b *PLBlock, c int) {
			p.Variables[p.Variables[c].Cursor.Arguments[0]].Role = PLOrdinarySlot
		},
		"argument_slot": func(p *PLProgram, b *PLBlock, c int) { p.Variables[c].Cursor.Arguments[0] = p.FoundSlot },
		"late_binding": func(p *PLProgram, b *PLBlock, c int) {
			p.Variables[c].Cursor.Scopes[1].Bindings["later_value"] = b.Bindings["later_value"]
		},
		"foreign_recipe": func(p *PLProgram, b *PLBlock, c int) {
			syntax := *p.Variables[c].Cursor.Syntax
			p.Variables[c].Cursor.Syntax = &syntax
		},
		"argument_instruction": func(p *PLProgram, b *PLBlock, c int) { b.Statements[0].Cursor.Arguments[0].Slot = p.FoundSlot },
		"fetch_target":         func(p *PLProgram, b *PLBlock, c int) { b.Statements[1].Cursor.Targets[0].Slot = c },
		"cursor_operand":       func(p *PLProgram, b *PLBlock, c int) { b.Statements[0].Cursor.Slot = p.FoundSlot },
		"loop_record_role": func(p *PLProgram, b *PLBlock, c int) {
			p.Variables[b.Statements[2].Cursor.LoopSlot].Role = PLOrdinarySlot
		},
		"loop_slot":    func(p *PLProgram, b *PLBlock, c int) { b.Statements[2].Cursor.LoopSlot = b.Bindings["n"] },
		"loop_binding": func(p *PLProgram, b *PLBlock, c int) { b.Statements[2].Cursor.Loop.Bindings["r"] = b.Bindings["n"] },
		"source":       func(p *PLProgram, b *PLBlock, c int) { p.Source = "BEGIN END" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d, p, _ := cursorProgramUnit(t)
			body := p.Root.Statements[0].Block
			mutate(p, body, body.Bindings["c"])
			if err := p.ValidateDefinition(d); err == nil {
				t.Fatal("forged cursor layout accepted")
			}
		})
	}
}

func TestPLpgSQLCursorLayoutEquivalence(t *testing.T) {
	_, p, _ := cursorProgramUnit(t)
	next := *p
	next.Variables = slices.Clone(p.Variables)
	if !p.SameLayout(&next) {
		t.Fatal("immutable layout copy differs")
	}
	body := p.Root.Statements[0].Block
	c := body.Bindings["c"]
	recipe := *next.Variables[c].Cursor
	recipe.Arguments = slices.Clone(recipe.Arguments)
	recipe.Arguments[0] = p.FoundSlot
	next.Variables[c].Cursor = &recipe
	if p.SameLayout(&next) {
		t.Fatal("recipe change ignored by equivalence")
	}
	if p.Variables[c].Cursor.Arguments[0] == p.FoundSlot {
		t.Fatal("copy mutated source")
	}
}

func TestPLpgSQLCursorFoundNamespace(t *testing.T) {
	for _, mode := range []string{"i", "o", "b"} {
		t.Run(mode, func(t *testing.T) {
			boolean := plUnitBuiltin(t, sqltype.Bool)
			body := "RETURN;"
			if mode == "i" {
				body = "RETURN found;"
			}
			d := &Definition{Kind: PLpgSQLBody, Name: "found_unit", Source: "DECLARE magic ALIAS FOR found; argument ALIAS FOR $1; copied found%TYPE; BEGIN " + body + " END", Result: boolean.Usage()}
			if err := d.SetFormals([]Formal{{Mode: mode, Name: "found", Usage: boolean.Usage()}}); err != nil {
				t.Fatal(err)
			}
			raw, err := parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			p, err := CompilePL(d, raw, false, plUnitCollation(t), func(parser.PLType) (sqltype.Usage, bool, error) { return boolean.Usage(), false, nil })
			if err != nil {
				t.Fatal(err)
			}
			d.PL, d.PLSyntax = p, raw
			if err = p.ValidateDefinition(d); err != nil {
				t.Fatal(err)
			}
			local := p.Root.Statements[0].Block
			if p.Root.Bindings["found"] != 0 || p.Root.Bindings["$1"] != 0 || p.FoundSlot != 1 || local.Bindings["magic"] != p.FoundSlot || local.Bindings["argument"] != 0 {
				t.Fatal("canonical formal map/alias overlay")
			}
			for _, name := range [][]string{{"found"}, {"found_unit", "found"}, {"magic"}} {
				if slot, ok := p.Lookup(name, []*PLBlock{p.Root, local}); !ok || slot != p.FoundSlot {
					t.Fatal("implicit FOUND lookup", name, slot, ok)
				}
			}
			if slot, ok := p.Lookup([]string{"$1"}, []*PLBlock{p.Root, local}); !ok || slot != 0 {
				t.Fatal("numeric formal lost")
			}
			shadow := &PLBlock{Bindings: map[string]int{"found": local.Bindings["copied"]}}
			if slot, ok := p.Lookup([]string{"found"}, []*PLBlock{p.Root, local, shadow}); !ok || slot != local.Bindings["copied"] {
				t.Fatal("local did not shadow magic FOUND")
			}
			if mode != "i" && (len(d.OutputFormals) != 1 || d.OutputFormals[0] != 0) {
				t.Fatal("OUT/INOUT output redirected to magic slot")
			}
			p.Root.Bindings["found"] = p.FoundSlot
			if err = p.ValidateDefinition(d); err == nil {
				t.Fatal("overwritten canonical formal accepted")
			}
		})
	}
}

func TestPLpgSQLCursorArgumentSyntax(t *testing.T) {
	for _, actual := range []string{"x", "x+1", "x::integer", "a=>x", "a:=x"} {
		t.Run(actual, func(t *testing.T) {
			for _, instruction := range []string{"OPEN c(" + actual + ");", "FOR r IN c(" + actual + ") LOOP END LOOP;"} {
				raw, err := parser.ParsePLpgSQL("DECLARE x integer:=7; c CURSOR(a integer) FOR SELECT a; BEGIN "+instruction+" RETURN 0; END", parser.PLReturnPolicy{})
				if err != nil {
					t.Fatal(err)
				}
				arg := raw.Statements[0].Cursor.Arguments[0]
				named := actual == "a=>x" || actual == "a:=x"
				if (arg.Name.Text != "") != named || named && arg.Name.Text != "a" {
					t.Fatal("argument notation lost", arg)
				}
				if arg.Expression == nil {
					t.Fatal("argument expression lost")
				}
			}
		})
	}
}

func TestPLpgSQLCursorOperandIdentity(t *testing.T) {
	refcursor := plUnitBuiltin(t, sqltype.Refcursor)
	integer := plUnitBuiltin(t, sqltype.Int4)
	boolean := plUnitBuiltin(t, sqltype.Bool)
	domain, err := sqltype.Domain(96001, "cursor_domain", "cursor_domain", refcursor.Usage())
	if err != nil {
		t.Fatal(err)
	}
	if !domain.Usage().Valid() || domain.ID() == sqltype.Refcursor || domain.BaseType() != refcursor {
		t.Fatal("invalid domain operand fixture")
	}
	for _, tc := range []struct{ name, statement string }{
		{"open_static", "OPEN c FOR SELECT 7;"},
		{"open_dynamic", "OPEN c FOR EXECUTE 'SELECT $1::integer' USING 7;"},
		{"fetch", "FETCH c INTO x;"},
		{"move", "MOVE c;"},
		{"close", "CLOSE c;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Definition{Kind: PLpgSQLBody, Name: "operand_unit", Source: "DECLARE c operand_type; x integer; BEGIN " + tc.statement + " RETURN x; END", Result: integer.Usage()}
			raw, err := parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			resolver := func(operand *sqltype.Descriptor) PLTypeResolver {
				return func(raw parser.PLType) (sqltype.Usage, bool, error) {
					switch raw.Type.Name {
					case "bool", "boolean":
						return boolean.Usage(), false, nil
					case "integer", "int4":
						return integer.Usage(), false, nil
					case "operand_type":
						return operand.Usage(), false, nil
					default:
						return sqltype.Usage{}, false, fmt.Errorf("unexpected type %s", raw.Type.Name)
					}
				}
			}
			_, err = CompilePL(d, raw, false, plUnitCollation(t), resolver(domain))
			var diagnostic *sqlstate.Error
			position := strconv.Itoa(raw.Statements[0].Cursor.Variable.SourcePosition().Character)
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Message != `variable "c" must be of type cursor or refcursor` || diagnostic.Position != position {
				t.Fatal("domain cursor operand was not rejected at its source position", err)
			}
			program, err := CompilePL(d, raw, false, plUnitCollation(t), resolver(refcursor))
			if err != nil {
				t.Fatal(err)
			}
			d.PLSyntax, d.PL = raw, program
			if err := program.ValidateDefinition(d); err != nil {
				t.Fatal("genuine refcursor rejected", err)
			}
			slot := program.Root.Statements[0].Block.Bindings["c"]
			forged := *program
			forged.Variables = slices.Clone(program.Variables)
			forged.Variables[slot].Usage = domain.Usage()
			candidate := *d
			candidate.PL = &forged
			if err := forged.ValidateDefinition(&candidate); err == nil {
				t.Fatal("checked domain operand admitted")
			}
			if program.Variables[slot].Usage.Type != refcursor {
				t.Fatal("forgery mutated source program")
			}
			if err := program.ValidateDefinition(d); err != nil {
				t.Fatal("source program changed", err)
			}
		})
	}
}

func TestPLpgSQLCursorDiagnosticFoundNamespace(t *testing.T) {
	integer, boolean := plUnitBuiltin(t, sqltype.Int4), plUnitBuiltin(t, sqltype.Bool)
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "item", Usage: integer.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	program := &PLProgram{FormalCount: 1, FoundSlot: 1, Variables: []PLVariable{
		{Name: "found", Usage: shape.Usage()},
		{Name: "found", Usage: boolean.Usage(), Role: PLFoundSlot},
		{Name: "found", Record: true},
	}}
	root := &PLBlock{Label: "fn", FunctionScope: true, Bindings: map[string]int{"found": 0, "$1": 0}}
	local := &PLBlock{Bindings: map[string]int{"found": 2}}
	for _, tc := range []struct {
		names        []string
		local, found bool
		slot, used   int
	}{
		{[]string{"found", "item"}, false, false, 0, 0},
		{[]string{"fn", "found", "item"}, false, false, 0, 0},
		{[]string{"$1", "item"}, false, true, 0, 1},
		{[]string{"fn", "$1", "item"}, false, true, 0, 2},
		{[]string{"found", "item"}, true, true, 2, 1},
		{[]string{"fn", "found", "item"}, true, false, 0, 0},
	} {
		scopes := []*PLBlock{root}
		if tc.local {
			scopes = append(scopes, local)
		}
		parts := make([]parser.Identifier, len(tc.names))
		for i, name := range tc.names {
			parts[i].Text = name
		}
		slot, used, found, err := plDiagnosticParent(program, parts, scopes)
		if err != nil || found != tc.found || found && (slot != tc.slot || used != tc.used) {
			t.Fatalf("diagnostic namespace %v local=%t got=%d/%d/%t err=%v", tc.names, tc.local, slot, used, found, err)
		}
	}
	if len(root.Bindings) != 2 || root.Bindings["found"] != 0 || root.Bindings["$1"] != 0 {
		t.Fatal("diagnostic lookup mutated canonical formals")
	}
}
