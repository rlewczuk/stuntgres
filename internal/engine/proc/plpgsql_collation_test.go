package proc

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLpgSQLFrameInputCollation(t *testing.T) {
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
		}
	}
	if text == nil {
		t.Fatal("missing text")
	}
	unicode, err := sqltype.RegisteredCollation(6411, 6411)
	if err != nil {
		t.Fatal(err)
	}
	c, err := sqltype.RegisteredCollation(950, 950)
	if err != nil {
		t.Fatal(err)
	}
	fixed := text.Usage()
	fixed.Collation = c
	d := &Definition{Kind: PLpgSQLBody, Name: "frame_collation", Result: text.Usage(), Source: "DECLARE a ALIAS FOR $1; y text := x; z y%TYPE; u source.s%TYPE; v u%TYPE; e text COLLATE \"C\"; f y%TYPE COLLATE \"C\"; g source.s%TYPE COLLATE pg_catalog.pg_unicode_fast; h e%TYPE; BEGIN RETURN a; END"}
	if err := d.SetFormals([]Formal{{Mode: "i", Name: "x", Usage: text.Usage()}}); err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	boolean := plUnitBuiltin(t, sqltype.Bool).Usage()
	p, err := CompilePL(d, raw, false, plUnitCollation(t), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "boolean" || raw.Type.Name == "bool" {
			return boolean, false, nil
		}
		if len(raw.Copy.Parts) != 0 {
			return fixed, false, nil
		}
		return text.Usage(), false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d.PLSyntax, d.PL = raw, p
	if err := p.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	input := NewFrame(d.Inputs)
	defer input.Close()
	value := sqltype.String(text, "Straße")
	if err := input.Bind(0, value, text.Usage()); err != nil {
		t.Fatal(err)
	}
	first := NewPLFrame(p, d, input, unicode)
	defer first.Close()
	second := NewPLFrame(p, d, input, c)
	defer second.Close()
	ordinary := NewPLFrame(p, d, input, sqltype.CollationRef{})
	defer ordinary.Close()
	body := p.Root.Statements[0].Block
	if body.Bindings["a"] != 0 {
		t.Fatal("alias acquired a separate collation slot")
	}
	for _, name := range []string{"x", "y", "z"} {
		slot := 0
		if name != "x" {
			slot = body.Bindings[name]
		}
		if !p.Variables[slot].InheritInputCollation || first.Usage(slot).Collation != unicode || second.Usage(slot).Collation != c || ordinary.Usage(slot) != p.Variables[slot].Usage {
			t.Fatal("effective invocation usage/provenance", name)
		}
		if p.Variables[slot].Usage.Collation != text.Usage().Collation {
			t.Fatal("activation mutated published usage")
		}
	}
	for _, name := range []string{"u", "v", "e", "f", "h"} {
		slot := body.Bindings[name]
		if p.Variables[slot].InheritInputCollation || first.Usage(slot) != fixed || second.Usage(slot) != fixed {
			t.Fatal("column/local copy lost fixed source provenance", name)
		}
	}
	g := body.Bindings["g"]
	if p.Variables[g].InheritInputCollation || first.Usage(g).Collation != unicode || second.Usage(g).Collation != unicode || second.Usage(body.Bindings["u"]) != fixed {
		t.Fatal("copied explicit override mutated source or inherited invocation")
	}
	second.Close()
	if second.InputCollation.ID != 0 || len(second.Values) != 0 || first.Usage(0).Collation != unicode || input.Parameters[0] != value {
		t.Fatal("nested/sibling Close leaked authority or mutated caller")
	}
	for _, name := range []string{"y", "z", "u", "v", "e", "f", "g", "h"} {
		slot := body.Bindings[name]
		p.Variables[slot].InheritInputCollation = !p.Variables[slot].InheritInputCollation
		if err := p.ValidateDefinition(d); err == nil {
			t.Fatal("forged collation provenance accepted", name)
		}
		p.Variables[slot].InheritInputCollation = !p.Variables[slot].InheritInputCollation
	}
	for _, decl := range raw.Declarations {
		if decl.Collation != nil {
			pos := decl.Collation.Parts[0].Pos.Offset
			if pos < 0 || pos+7 > len(d.Source) || d.Source[pos:pos+7] != "COLLATE" {
				t.Fatal("lost collation keyword position", pos)
			}
		}
	}
	slot := body.Bindings["e"]
	saved := p.Variables[slot].Usage.Collation
	for _, bad := range []sqltype.CollationRef{{}, {ID: 97000}} {
		p.Variables[slot].Usage.Collation = bad
		if err := p.ValidateDefinition(d); err == nil {
			t.Fatal("forged explicit collation accepted", bad)
		}
	}
	p.Variables[slot].Usage.Collation = saved
}
