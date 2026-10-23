package stored

import (
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLConditionalRemap(t *testing.T) {
	read, source, types, collations := plpgsqlLocalUsageFixture(t)
	definition := *source.SQL
	definition.Source = "DECLARE x local_type; BEGIN CASE x WHEN 'x' THEN RETURN 1; ELSE RETURN 2; END CASE; END"
	raw, err := parser.ParsePLpgSQL(definition.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	original := source.SQL.PL.Variables[0].Usage
	definition.PLSyntax = raw
	definition.PL, err = proc.CompilePL(&definition, raw, false, func(parser.PLType) (sqltype.Usage, bool, error) { return original, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.PL.ValidateDefinition(&definition); err != nil {
		t.Fatal(err)
	}
	conditional := *source
	conditional.SQL = &definition
	source = &conditional
	read.State().SetDescriptor(source.Address(), source)
	input := proc.NewFrame(nil)
	defer input.Close()
	frame := proc.NewPLFrame(definition.PL, &definition, input, sqltype.CollationRef{})
	defer frame.Close()
	frame.Searches[0] = proc.PLSearch{Value: sqltype.Null(original.Type), Usage: original}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[0].ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())},
		{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(types[2].ID())},
		collations[0].Address(): collations[1].Address(),
		collations[1].Address(): collations[2].Address(),
	}
	mapType := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		address, changed := mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.ID())}]
		if !changed {
			return old
		}
		target, ok := read.Type(address)
		if !ok {
			return nil
		}
		return target.SQLType
	}
	remap := NewRoutineRemap(read, mapping, mapType, nil, false)
	if err := routine.RebindSQLDefinitions(read.State(), remap); err != nil {
		t.Fatal(err)
	}
	next, ok := read.Procedure(source.Address())
	if !ok {
		t.Fatal("conditional routine lost")
	}
	if captured, err := remap.Definition(source.SQL); err != nil || captured != next.SQL {
		t.Fatal("conditional definition lost operation-owned remap identity", err)
	}
	want := original
	want.Type = types[1]
	want.Collation, err = collations[1].Usage()
	if err != nil {
		t.Fatal(err)
	}
	if len(next.SQL.PL.Variables) != 1 || next.SQL.PL.SearchCount != 1 || next.SQL.PL.Variables[0].Usage != want || source.SQL.PL.Variables[0].Usage != original || frame.Searches[0].Usage != original {
		t.Fatal("search state polluted declaration remap or captured frame mutated")
	}
	fresh := proc.NewPLFrame(next.SQL.PL, next.SQL, input, sqltype.CollationRef{})
	defer fresh.Close()
	if fresh.Searches[0].Value.Type() != nil || fresh.Searches[0].Usage != (sqltype.Usage{}) {
		t.Fatal("replacement serialized an invocation search Usage")
	}
	if same, err := sameMappedSQLRoutine(next, source, mapType, typedef.RemappedCollationCapabilities(read.State(), mapping)); err != nil || !same {
		t.Fatal("equivalent once-mapped conditional local Usage rejected", err)
	}
	programs := make(map[*proc.PLProgram]*proc.PLProgram)
	if _, err := reownRoutineDefinition(source.SQL, func(usage sqltype.Usage) (sqltype.Usage, error) {
		if usage.Type == types[0] {
			usage.Type = nil
		}
		return usage, nil
	}, programs); err == nil {
		t.Fatal("missing local type mapping silently defaulted")
	}
	if len(programs) != 0 {
		t.Fatal("failed local mapping published a program memo entry")
	}
	if err := source.SQL.PL.ValidateDefinition(source.SQL); err != nil {
		t.Fatal("remap mutated checked source", err)
	}
}
