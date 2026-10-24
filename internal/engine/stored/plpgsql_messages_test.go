package stored

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLRaiseAssertRemap(t *testing.T) {
	read, source, types, collations := plpgsqlLocalUsageFixture(t)
	definition := *source.SQL
	definition.Source = "DECLARE x local_type; BEGIN RAISE NOTICE '%',x USING DETAIL=x,HINT=x; ASSERT x IS NULL,x; RETURN 7; END"
	raw, err := parser.ParsePLpgSQL(definition.Source, parser.PLReturnPolicy{})
	if err != nil { t.Fatal(err) }
	original := source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["x"]].Usage
	definition.PLSyntax = raw
	definition.PL, err = proc.CompilePL(&definition, raw, false, plpgsqlUnitCollation(t, read), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "bool" || raw.Type.Name == "boolean" {
			boolean, _ := read.LookupType(sqltype.Bool)
			return boolean.Usage(), false, nil
		}
		return original, false, nil
	})
	if err != nil { t.Fatal(err) }
	if err := definition.PL.ValidateDefinition(&definition); err != nil { t.Fatal(err) }
	candidate := *source
	candidate.SQL = &definition
	source = &candidate
	read.State().SetDescriptor(source.Address(), source)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		{ClassID:ids.TypeClassID,ObjectID:ids.OID(types[0].ID())}: {ClassID:ids.TypeClassID,ObjectID:ids.OID(types[1].ID())},
		{ClassID:ids.TypeClassID,ObjectID:ids.OID(types[1].ID())}: {ClassID:ids.TypeClassID,ObjectID:ids.OID(types[2].ID())},
		collations[0].Address():collations[1].Address(),
		collations[1].Address():collations[2].Address(),
	}
	mapType := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil { return nil }
		address, changed := mapping[ids.ObjectAddress{ClassID:ids.TypeClassID,ObjectID:ids.OID(old.ID())}]
		if !changed { return old }
		target, ok := read.Type(address)
		if !ok { return nil }
		return target.SQLType
	}
	remap := NewRoutineRemap(read, mapping, mapType, nil, false)
	if err := routine.RebindSQLDefinitions(read.State(), remap); err != nil { t.Fatal(err) }
	next, ok := read.Procedure(source.Address())
	if !ok { t.Fatal("mapped routine lost") }
	if captured, err := remap.Definition(source.SQL); err != nil || captured != next.SQL { t.Fatal("remap lost shared definition identity", err) }
	want := original
	want.Type = types[1]
	want.Collation, err = collations[1].Usage()
	if err != nil { t.Fatal(err) }
	local := definition.PL.Root.Statements[0].Block.Bindings["x"]
	if next.SQL.PL.Variables[local].Usage != want || definition.PL.Variables[local].Usage != original || !definition.PL.SameLayout(next.SQL.PL) { t.Fatal("once-mapped complete Usage/layout or source immutability") }
	if next.SQL.PL.Root != definition.PL.Root || !reflect.DeepEqual(next.SQL.PLSyntax, raw) { t.Fatal("immutable message queries copied or changed") }
	if err := next.SQL.PL.ValidateDefinition(next.SQL); err != nil { t.Fatal("remapped definition invalid", err) }
	if same, err := sameMappedSQLRoutine(next, source, mapType, typedef.RemappedCollationCapabilities(read.State(), mapping)); err != nil || !same { t.Fatal("mapped message declaration not equivalent", err) }
	count := 0
	_, err = reownRoutineDefinition(&definition, func(u sqltype.Usage) (sqltype.Usage, error) {
		if u.Type == types[0] { count++; return want,nil }
		return u,nil
	}, make(map[*proc.PLProgram]*proc.PLProgram))
	if err != nil || count != 1 { t.Fatal("local Usage mapped more than once", count, err) }
}
