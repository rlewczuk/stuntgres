package stored

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLDiagnosticsRemapOwnership(t *testing.T) {
	read, source, types, collations := plpgsqlLocalUsageFixture(t)
	definition := *source.SQL
	definition.Source = "DECLARE x local_type; a ALIAS FOR x; BEGIN GET DIAGNOSTICS a=ROW_COUNT,x=PG_CONTEXT; BEGIN RAISE EXCEPTION 'caught'; EXCEPTION WHEN OTHERS THEN GET STACKED DIAGNOSTICS a=MESSAGE_TEXT,x=PG_EXCEPTION_CONTEXT; END; RETURN 7; END"
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
		if raw.Type.Name == "text" {
			text, _ := read.LookupType(sqltype.Text)
			return text.Usage(), false, nil
		}
		return original, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := definition.PL.ValidateDefinition(&definition); err != nil {
		t.Fatal(err)
	}
	candidate := *source
	candidate.SQL = &definition
	source = &candidate
	read.State().SetDescriptor(source.Address(), source)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[0].ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())},
		{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(types[2].ID())},
		collations[0].Address(): collations[1].Address(), collations[1].Address(): collations[2].Address(),
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
	program := definition.PL
	variables := append([]proc.PLVariable(nil), program.Variables...)
	remap := NewRoutineRemap(read, mapping, mapType, nil, false)
	if err := routine.RebindSQLDefinitions(read.State(), remap); err != nil {
		t.Fatal(err)
	}
	next, ok := read.Procedure(source.Address())
	if !ok || next == source || next.SQL == &definition || next.SQL.PL == program {
		t.Fatal("remap did not publish distinct typed owner")
	}
	if captured, err := remap.Definition(source.SQL); err != nil || captured != next.SQL {
		t.Fatal("operation-wide remap lost shared definition identity", err)
	}
	want := original
	want.Type = types[1]
	want.Collation, err = collations[1].Usage()
	if err != nil {
		t.Fatal(err)
	}
	local := program.Root.Statements[0].Block.Bindings["x"]
	if next.SQL.PL.Variables[local].Usage != want || !reflect.DeepEqual(program.Variables, variables) || program.Variables[local].Usage != original || !program.SameLayout(next.SQL.PL) {
		t.Fatal("remap changed source or mapped complete Usage more than once")
	}
	if next.SQL.PL.Root != program.Root || next.SQL.PLSyntax != raw || !reflect.DeepEqual(next.SQL.PLSyntax, raw) {
		t.Fatal("remap copied or replaced immutable diagnostics target/item backing")
	}
	if err := next.SQL.PL.ValidateDefinition(next.SQL); err != nil {
		t.Fatal("mapped diagnostics invalid", err)
	}
	if same, err := sameMappedSQLRoutine(next, source, mapType, typedef.RemappedCollationCapabilities(read.State(), mapping)); err != nil || !same {
		t.Fatal("mapped diagnostics declaration mismatch", err)
	}
	count := 0
	if _, err := reownRoutineDefinition(&definition, func(u sqltype.Usage) (sqltype.Usage, error) {
		if u.Type == types[0] {
			count++
			return want, nil
		}
		return u, nil
	}, make(map[*proc.PLProgram]*proc.PLProgram)); err != nil || count != 1 {
		t.Fatal("alias duplicated typed authority", count, err)
	}
	// Reject a coordinated parser/checked mutation before publishing any new
	// definition. The genuine previous owner and its immutable backing survive.
	bad := definition
	bad.PLSyntax, err = parser.ParsePLpgSQL(bad.Source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	bad.PL, err = proc.CompilePL(&bad, bad.PLSyntax, false, plpgsqlUnitCollation(t, read), func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "bool" || raw.Type.Name == "boolean" {
			boolean, _ := read.LookupType(sqltype.Bool)
			return boolean.Usage(), false, nil
		}
		if raw.Type.Name == "text" {
			text, _ := read.LookupType(sqltype.Text)
			return text.Usage(), false, nil
		}
		return original, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.PL.ValidateDefinition(&bad); err != nil {
		t.Fatal("candidate invalid before forgery", err)
	}
	instruction := &bad.PL.Root.Statements[0].Block.Statements[0]
	instruction.Diagnostics.Assignments[0].Item = parser.PLContext
	instruction.Syntax.Diagnostics.Assignments[0].Item = parser.PLContext
	if _, err := NewRoutineRemap(read, mapping, mapType, nil, false).Definition(&bad); err == nil {
		t.Fatal("forged diagnostics remap admitted")
	}
	t.Run("separately_admitted", func(t *testing.T) {
		bad := definition
		bad.PLSyntax, err = parser.ParsePLpgSQL(bad.Source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		bad.PL, err = proc.CompilePL(&bad, bad.PLSyntax, false, plpgsqlUnitCollation(t, read), func(raw parser.PLType) (sqltype.Usage, bool, error) {
			if raw.Type.Name == "bool" || raw.Type.Name == "boolean" {
				boolean, _ := read.LookupType(sqltype.Bool)
				return boolean.Usage(), false, nil
			}
			if raw.Type.Name == "text" {
				text, _ := read.LookupType(sqltype.Text)
				return text.Usage(), false, nil
			}
			return original, false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := bad.PL.ValidateDefinition(&bad); err != nil {
			t.Fatal("candidate invalid before substitution", err)
		}
		instruction := &bad.PL.Root.Statements[0].Block.Statements[0]
		prior := instruction.Syntax.Diagnostics
		sourceText := bad.Source
		donor, err := parser.ParsePLpgSQL(strings.Replace(sourceText, "x=PG_CONTEXT", "x=ROW_COUNT", 1), parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		replacement := donor.Statements[0].Diagnostics
		if replacement == prior || !parser.ValidPLDiagnostics(replacement) || replacement.Pos != prior.Pos || replacement.Assignments[0].ItemPos != prior.Assignments[0].ItemPos || replacement.Assignments[1].ItemPos != prior.Assignments[1].ItemPos {
			t.Fatal("donor is not a separately admitted coordinate-preserving payload")
		}
		instruction.Syntax.Diagnostics = replacement
		instruction.Diagnostics.Assignments[1].Item = parser.PLRowCount
		if bad.Source != sourceText || bad.PLSyntax.Statements[0].Diagnostics != replacement || prior.Assignments[1].Item != parser.PLContext || !parser.ValidPLDiagnostics(prior) {
			t.Fatal("substitution changed source or prior parser backing")
		}
		if _, err := NewRoutineRemap(read, mapping, mapType, nil, false).Definition(&bad); err == nil {
			t.Fatal("separately admitted foreign-source diagnostics remap accepted")
		}
		current, ok := read.Procedure(source.Address())
		if !ok || current != next || current.SQL.PLSyntax != raw || current.SQL.PL.Root != program.Root || !reflect.DeepEqual(program.Variables, variables) || program.ValidateDefinition(&definition) != nil {
			t.Fatal("rejected substitution changed previously published definition/backing")
		}
	})
	missingGeneration := NewRoutineRemap(read, mapping, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == types[0] {
			return nil
		}
		return old
	}, nil, false)
	if _, err := missingGeneration.Definition(&definition); err == nil {
		t.Fatal("missing original descriptor generation admitted")
	}
	current, ok := read.Procedure(source.Address())
	if !ok || current != next || current.SQL.PL.Root != program.Root || !reflect.DeepEqual(program.Variables, variables) || program.ValidateDefinition(&definition) != nil {
		t.Fatal("rejected remap changed published/source backing")
	}
}
