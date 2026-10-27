package stored

import (
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLpgSQLDynamicSQLOwnership(t *testing.T) {
	read, source, types, _ := plpgsqlLocalUsageFixture(t)
	definition := *source.SQL
	definition.Source = "DECLARE x local_type; BEGIN EXECUTE x INTO x USING x; RETURN 1; END"
	raw, err := parser.ParsePLpgSQL(definition.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	original := source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["x"]].Usage
	definition.PLSyntax = raw
	definition.PL, err = proc.CompilePL(&definition, raw, false, plpgsqlUnitCollation(t, read), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "boolean" || raw.Type.Name == "bool" {
			v, _ := read.LookupType(sqltype.Bool)
			return v.Usage(), false, nil
		}
		return original, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := original
	want.Type = types[1]
	count := 0
	mapped, err := reownRoutineDefinition(&definition, func(u sqltype.Usage) (sqltype.Usage, error) {
		if u.Type == types[0] {
			count++
			return want, nil
		}
		return u, nil
	}, make(map[*proc.PLProgram]*proc.PLProgram))
	if err != nil {
		t.Fatal(err)
	}
	local := definition.PL.Root.Statements[0].Block.Bindings["x"]
	if count != 1 || mapped == &definition || mapped.PL == definition.PL || mapped.PL.Variables[local].Usage != want || definition.PL.Variables[local].Usage != original || mapped.PL.Root != definition.PL.Root || mapped.PLSyntax != raw || !mapped.PL.SameLayout(definition.PL) {
		t.Fatal("dynamic syntax retained invocation types or remapped Usage twice", count)
	}
	if mapped.PL.Root.Statements[0].Block.Statements[0].Dynamic != definition.PL.Root.Statements[0].Block.Statements[0].Dynamic || mapped.PL.ValidateDefinition(mapped) != nil || definition.PL.ValidateDefinition(&definition) != nil {
		t.Fatal("immutable checked instruction changed during reownership")
	}
}
