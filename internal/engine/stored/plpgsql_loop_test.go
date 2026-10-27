package stored

import (
	"testing"

	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLLoopRemap(t *testing.T) {
	read, source, types, collations := plpgsqlLocalUsageFixture(t)
	definition := *source.SQL
	definition.Source = "DECLARE x local_type; BEGIN <<o>> LOOP CONTINUE o WHEN x='again'; EXIT o WHEN x IS NULL; END LOOP o; RETURN 7; END"
	raw, err := parser.ParsePLpgSQL(definition.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	original := source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["x"]].Usage
	definition.PLSyntax = raw
	definition.PL, err = proc.CompilePL(&definition, raw, false, plpgsqlUnitCollation(t, read), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "bool" || raw.Type.Name == "boolean" {
			boolean, _ := read.LookupType(sqltype.Bool)
			return boolean.Usage(), false, nil
		}
		return original, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.PL.ValidateDefinition(&definition); err != nil {
		t.Fatal(err)
	}
	want := original
	want.Type = types[1]
	want.Collation, err = collations[1].Usage()
	if err != nil {
		t.Fatal(err)
	}
	mapped := 0
	programs := make(map[*proc.PLProgram]*proc.PLProgram)
	next, err := reownRoutineDefinition(&definition, func(usage sqltype.Usage) (sqltype.Usage, error) {
		if usage.Type == types[0] {
			mapped++
			usage = want
		}
		return usage, nil
	}, programs)
	if err != nil {
		t.Fatal(err)
	}
	local := definition.PL.Root.Statements[0].Block.Bindings["x"]
	if mapped != 1 || next.PL == definition.PL || next.PL.Variables[local].Usage != want || definition.PL.Variables[local].Usage != original {
		t.Fatal("local Usage not mapped exactly once or source mutated", mapped)
	}
	loop := definition.PL.Root.Statements[0].Block.Statements[0].Loop
	replacement := next.PL.Root.Statements[0].Block.Statements[0].Loop
	if loop != replacement || replacement.Control != replacement.Statements[0].Transfer || replacement.Control != replacement.Statements[1].Transfer || !definition.PL.SameLayout(next.PL) {
		t.Fatal("immutable control graph changed during Usage remap")
	}
	if err := next.PL.ValidateDefinition(next); err != nil {
		t.Fatal("replacement failed checked ownership", err)
	}
	if err := definition.PL.ValidateDefinition(&definition); err != nil {
		t.Fatal("source invalidated by replacement", err)
	}
	input := proc.NewFrame(nil)
	defer input.Close()
	frame := proc.NewPLFrame(next.PL, next, input, sqltype.CollationRef{})
	defer frame.Close()
	if frame.Values[local].Type() != nil || len(frame.Scopes) != 0 {
		t.Fatal("replacement retained invocation/condition state")
	}
}

func TestPLpgSQLWhileRemap(t *testing.T) {
	read, source, types, collations := plpgsqlLocalUsageFixture(t)
	definition := *source.SQL
	definition.Source = "DECLARE x local_type; BEGIN <<o>> WHILE x IS NULL LOOP x:='again'; CONTINUE o; END LOOP o; RETURN 7; END"
	raw, err := parser.ParsePLpgSQL(definition.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	original := source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["x"]].Usage
	definition.PLSyntax = raw
	definition.PL, err = proc.CompilePL(&definition, raw, false, plpgsqlUnitCollation(t, read), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "bool" || raw.Type.Name == "boolean" {
			boolean, _ := read.LookupType(sqltype.Bool)
			return boolean.Usage(), false, nil
		}
		return original, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.PL.ValidateDefinition(&definition); err != nil {
		t.Fatal(err)
	}
	want := original
	want.Type = types[1]
	want.Collation, err = collations[1].Usage()
	if err != nil {
		t.Fatal(err)
	}
	mapped := 0
	programs := make(map[*proc.PLProgram]*proc.PLProgram)
	next, err := reownRoutineDefinition(&definition, func(usage sqltype.Usage) (sqltype.Usage, error) {
		if usage.Type == types[0] {
			mapped++
			return want, nil
		}
		return usage, nil
	}, programs)
	if err != nil {
		t.Fatal(err)
	}
	local := definition.PL.Root.Statements[0].Block.Bindings["x"]
	if mapped != 1 || next.PL == definition.PL || next.PL.Variables[local].Usage != want || definition.PL.Variables[local].Usage != original {
		t.Fatal("WHILE local Usage not mapped once or original mutated", mapped)
	}
	loop := definition.PL.Root.Statements[0].Block.Statements[0].While
	replacement := next.PL.Root.Statements[0].Block.Statements[0].While
	if loop != replacement || replacement.Condition != loop.Condition || replacement.Body != loop.Body || replacement.Body.Control != replacement.Body.Statements[1].Transfer || !definition.PL.SameLayout(next.PL) || next.PLSyntax != definition.PLSyntax {
		t.Fatal("WHILE immutable condition/syntax/control graph changed during remap")
	}
	if err := next.PL.ValidateDefinition(next); err != nil {
		t.Fatal("mapped WHILE invalid", err)
	}
	if err := definition.PL.ValidateDefinition(&definition); err != nil {
		t.Fatal("original WHILE invalidated", err)
	}
	input := proc.NewFrame(nil)
	defer input.Close()
	frame := proc.NewPLFrame(next.PL, next, input, sqltype.CollationRef{})
	defer frame.Close()
	if frame.Values[local].Type() != nil || len(frame.Scopes) != 0 {
		t.Fatal("mapped WHILE retained invocation state")
	}
}
