package stored

import (
	"testing"

	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLIntegerForRemap(t *testing.T) {
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
	d := &proc.Definition{Kind: proc.PLpgSQLBody, Name: "integer_for_remap", Source: `DECLARE x local_type; BEGIN <<n>> FOR i IN 1..3 BY 1 LOOP CONTINUE n; END LOOP n; RETURN 1; END`, Result: types[sqltype.Int4].Usage()}
	d.PLSyntax, err = parser.ParsePLpgSQL(d.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d.PL, err = proc.CompilePL(d, d.PLSyntax, false, func(u sqltype.Usage, _ *parser.ObjectName) (sqltype.Usage, error) { return u, nil }, func(raw parser.PLType) (sqltype.Usage, bool, error) {
		switch raw.Type.Name {
		case "bool", "boolean":
			return types[sqltype.Bool].Usage(), false, nil
		case "int4":
			return types[sqltype.Int4].Usage(), false, nil
		}
		return old.Usage(), false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	local := d.PL.Root.Statements[0].Block.Bindings["x"]
	loop := d.PL.Root.Statements[0].Block.Statements[0].IntegerFor
	mappedOld, mappedNext, integers := 0, 0, 0
	usage := func(u sqltype.Usage) (sqltype.Usage, error) {
		switch u.Type {
		case old:
			mappedOld++
			u.Type = next
		case next:
			mappedNext++
		}
		if u.Type.ID() == sqltype.Int4 {
			integers++
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
	// Two definition results plus ONE private counter Usage; shared-program
	// memoization must not visit any Variables again for the second consumer.
	if first.PL == d.PL || second.PL != first.PL || mappedOld != 1 || mappedNext != 0 || integers != 3 || first.PL.Variables[local].Usage.Type != next || d.PL.Variables[local].Usage.Type != old || !first.PL.SameLayout(d.PL) {
		t.Fatal("copy-on-write/shared remap", mappedOld, mappedNext, integers)
	}
	mapped := first.PL.Root.Statements[0].Block.Statements[0].IntegerFor
	if mapped != loop || mapped.Body.Statements[0].Transfer != mapped.Body.Control || first.PLSyntax != d.PLSyntax || first.Source != d.Source || mapped.Counter != loop.Counter || first.PL.Variables[loop.Counter].Role != proc.PLIntegerForSlot {
		t.Fatal("remap rewrote immutable range recipe/source")
	}
	if err := first.PL.ValidateDefinition(first); err != nil {
		t.Fatal(err)
	}
	if err := d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
}
