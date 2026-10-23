package proc

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func plUnitInteger(t *testing.T) *sqltype.Descriptor {
	t.Helper()
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			return typ
		}
	}
	t.Fatal("missing integer")
	return nil
}
func TestPLpgSQLFormalSlots(t *testing.T) {
	integer := plUnitInteger(t)
	d := &Definition{Name: "slots", Result: integer.Usage(), Source: "DECLARE a ALIAS FOR $1; BEGIN a := $2+1; RETURN; END"}
	if err := d.SetFormals([]Formal{{Mode: "o", Name: "y", Usage: integer.Usage()}, {Mode: "i", Name: "x", Usage: integer.Usage()}}); err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := CompilePL(d, raw, false, func(parser.PLType) (sqltype.Usage, bool, error) { return integer.Usage(), false, nil })
	if err != nil {
		t.Fatal(err)
	}
	d.Kind, d.PLSyntax, d.PL = PLpgSQLBody, raw, p
	if err := p.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	broken := *p
	root := *p.Root
	root.Bindings = map[string]int{"$2": 1}
	broken.Root = &root
	brokenDefinition := *d
	brokenDefinition.PL = &broken
	if err := broken.ValidateDefinition(&brokenDefinition); err == nil {
		t.Fatal("missing first all-formal binding accepted")
	}
	input := NewFrame(d.Inputs)
	defer input.Close()
	four, err := sqltype.Integer(integer, 4)
	if err != nil {
		t.Fatal(err)
	}
	if err = input.Bind(0, four, integer.Usage()); err != nil {
		t.Fatal(err)
	}
	f := NewPLFrame(p, d, input, sqltype.CollationRef{})
	defer f.Close()
	f.Enter(p.Root)
	body := p.Root.Statements[0].Block
	f.Enter(body)
	defer func() { f.Leave(body); f.Leave(p.Root) }()
	if p.FormalCount != 2 || body.Bindings["a"] != 0 || body.Statements[0].Target.Slot != 0 || !f.Values[0].IsNull() || f.Values[1] != four {
		t.Fatal("OUT/input/alias projection", p, f.Values)
	}
	if slot, ok := p.Lookup([]string{"$2"}, f.Scopes); !ok || slot != 1 {
		t.Fatal("PL input formal renumbered", slot, ok)
	}
	f.Values[body.Bindings["a"]] = four
	if f.Values[0] != four || input.Parameters[0] != four || len(d.Inputs) != 1 || d.FormalInputs[0] != -1 || d.FormalInputs[1] != 0 {
		t.Fatal("alias copied or caller layout changed")
	}
}

func TestPLpgSQLFrameLifetime(t *testing.T) {
	integer := plUnitInteger(t)
	d := &Definition{Name: "lifetime", Result: integer.Usage(), Source: "DECLARE x integer := 1; a ALIAS FOR x; BEGIN DECLARE x integer := 2; BEGIN x := 3; END; RETURN x; END"}
	raw, err := parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := CompilePL(d, raw, false, func(parser.PLType) (sqltype.Usage, bool, error) { return integer.Usage(), false, nil })
	if err != nil {
		t.Fatal(err)
	}
	input := NewFrame(nil)
	defer input.Close()
	f := NewPLFrame(p, d, input, sqltype.CollationRef{})
	outer := p.Root.Statements[0].Block
	inner := outer.Statements[0].Block
	f.Enter(p.Root)
	f.Enter(outer)
	x := outer.Bindings["x"]
	if outer.Bindings["a"] != x {
		t.Fatal("alias has private value slot")
	}
	f.Values[x], err = sqltype.Integer(integer, 9)
	if err != nil {
		t.Fatal(err)
	}
	f.Enter(inner)
	if slot, ok := p.Lookup([]string{"x"}, f.Scopes); !ok || slot == x {
		t.Fatal("shadow not selected")
	}
	f.Leave(inner)
	if slot, ok := p.Lookup([]string{"x"}, f.Scopes); !ok || slot != x {
		t.Fatal("outer slot not restored")
	}
	returned := f.Values[x]
	f.Leave(outer)
	f.Leave(p.Root)
	f.Close()
	f.Close()
	value, err := returned.Integer()
	if err != nil || value != 9 || len(f.Values) != 0 || len(f.Scopes) != 0 || p.Variables[x].Name != "x" || outer.Bindings["a"] != x {
		t.Fatal("result/immutable program lifetime", value, err)
	}
	fresh := NewPLFrame(p, d, input, sqltype.CollationRef{})
	defer fresh.Close()
	if fresh.Values[x] != (sqltype.Value{}) {
		t.Fatal("inactive local retained or preinitialized invocation value")
	}
}
