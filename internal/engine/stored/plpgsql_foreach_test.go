package stored

import (
	"testing"

	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLForeachRemap(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	old, err := sqltype.Domain(99001, "old_text", "old_text", types[sqltype.Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	next, err := sqltype.Domain(99002, "next_text", "next_text", types[sqltype.Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	last, err := sqltype.Domain(99003, "last_text", "last_text", types[sqltype.Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	d := &proc.Definition{Kind: proc.PLpgSQLBody, Name: "foreach_remap", Source: `DECLARE x local_type; BEGIN <<f>> FOREACH x IN ARRAY missing_array LOOP CONTINUE f; END LOOP f; RETURN 1; END`, Result: types[sqltype.Int4].Usage()}
	d.PLSyntax, err = parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d.PL, err = proc.CompilePL(d, d.PLSyntax, false, func(u sqltype.Usage, _ *parser.ObjectName) (sqltype.Usage, error) { return u, nil }, func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "boolean" || raw.Type.Name == "bool" {
			return types[sqltype.Bool].Usage(), false, nil
		}
		return old.Usage(), false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	local := d.PL.Root.Statements[0].Block.Bindings["x"]
	originalLoop := d.PL.Root.Statements[0].Block.Statements[0].Foreach
	mappedOld, mappedNext := 0, 0
	usage := func(u sqltype.Usage) (sqltype.Usage, error) {
		switch u.Type {
		case old:
			mappedOld++
			u.Type = next
		case next:
			mappedNext++
			u.Type = last
		}
		return u, nil
	}
	programs := make(map[*proc.PLProgram]*proc.PLProgram)
	first, err := reownRoutineDefinition(d, usage, programs)
	if err != nil {
		t.Fatal(err)
	}
	second, err := reownRoutineDefinition(d, usage, programs)
	if err != nil {
		t.Fatal(err)
	}
	if first.PL == d.PL || second.PL != first.PL || first.PL.Variables[local].Usage.Type != next || mappedOld != 1 || mappedNext != 0 || d.PL.Variables[local].Usage.Type != old || !d.PL.SameLayout(first.PL) {
		t.Fatal("FOREACH remap changed source or remapped a shared local twice", mappedOld, mappedNext)
	}
	loop := first.PL.Root.Statements[0].Block.Statements[0].Foreach
	if loop != originalLoop || loop.Body.Statements[0].Transfer != loop.Body.Control || loop.Targets[0].Slot != local || first.Source != d.Source || first.PLSyntax != d.PLSyntax {
		t.Fatal("FOREACH reownership rebuilt syntax/control or retained a descriptor outside Variables")
	}
	if err := first.PL.ValidateDefinition(first); err != nil {
		t.Fatal(err)
	}
	if err := d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
}
