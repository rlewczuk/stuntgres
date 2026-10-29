package stored

import (
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLpgSQLCursorRemap(t *testing.T) {
	for _, incompatible := range []bool{false, true} {
		name := "complete_usage_once"
		if incompatible {
			name = "mapping_error_atomicity"
		}
		t.Run(name, func(t *testing.T) {
			read, source, types, collations := plpgsqlLocalUsageFixture(t)
			usage := source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["x"]].Usage
			definition := *source.SQL
			definition.Source = "DECLARE x local_type COLLATE source_unicode; c CURSOR(a local_type) FOR SELECT a AS i; BEGIN OPEN c(x); FOR r IN c(x) LOOP END LOOP; RETURN 1; END"
			raw, err := parser.ParsePLpgSQL(definition.Source, parser.PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			definition.PLSyntax = raw
			definition.PL, err = proc.CompilePL(&definition, raw, false, plpgsqlUnitCollation(t, read), func(raw parser.PLType) (sqltype.Usage, bool, error) {
				switch raw.Type.Name {
				case "boolean", "bool":
					typ, _ := read.LookupType(sqltype.Bool)
					return typ.Usage(), false, nil
				case "refcursor":
					typ, _ := read.LookupType(sqltype.Refcursor)
					return typ.Usage(), false, nil
				default:
					return usage, false, nil
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = definition.PL.ValidateDefinition(&definition); err != nil {
				t.Fatal(err)
			}
			candidate := *source
			candidate.SQL = &definition
			source = &candidate
			read.State().SetDescriptor(source.Address(), source)
			original := slices.Clone(source.SQL.PL.Variables)
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{
				{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[0].ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())},
				{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())}: {ClassID: ids.TypeClassID, ObjectID: ids.OID(types[2].ID())},
				collations[0].Address(): collations[1].Address(),
				collations[1].Address(): collations[2].Address(),
			}
			if incompatible {
				mapping[collations[0].Address()] = ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950}
			}
			remap := NewRoutineRemap(read, mapping, nil, nil, false)
			err = routine.RebindRoutineDefinitions(read.State(), remap)
			if incompatible {
				if err == nil {
					t.Fatal("cursor argument collation mapping error swallowed")
				}
				current, _ := read.Procedure(source.Address())
				if current != source {
					t.Fatal("failed remap published candidate")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				next, ok := read.Procedure(source.Address())
				if !ok || next.SQL.PL == source.SQL.PL {
					t.Fatal("program not detached")
				}
				if !next.SQL.PL.SameLayout(source.SQL.PL) {
					t.Fatal("immutable cursor scopes/recipes changed while mapping Usage")
				}
				mappedCount := 0
				for i, variable := range original {
					want := variable.Usage
					if !variable.Record && want.Type == types[0] {
						mappedCount++
						want.Type = types[1]
						want.Collation, err = collations[1].Usage()
						if err != nil {
							t.Fatal(err)
						}
					}
					if got := next.SQL.PL.Variables[i].Usage; got != want {
						t.Fatalf("slot %d role %d Usage=%#v want=%#v", i, variable.Role, got, want)
					}
					if want.Type == types[1] {
						got := next.SQL.PL.Variables[i].Usage
						if !got.Valid() || got.Typmod != -1 || got.Type.DomainBase().Typmod != 16 {
							t.Fatal("mapped domain/base Usage invalid", i, got)
						}
					}
				}
				if mappedCount != 2 {
					t.Fatal("local and cursor-argument Usage were not both exercised", mappedCount)
				}
				if err = next.SQL.PL.ValidateDefinition(next.SQL); err != nil {
					t.Fatal("mapped complete layout invalid", err)
				}
			}
			if !slices.Equal(source.SQL.PL.Variables, original) {
				t.Fatal("remap mutated source Variables")
			}
			// Recipe/scopes have no typed descriptors outside Variables and can
			// remain shared immutably across catalog generations.
			if source.SQL.PL.Variables[source.SQL.PL.Root.Statements[0].Block.Bindings["c"]].Cursor.Syntax != raw.Declarations[1].Cursor {
				t.Fatal("source syntax association mutated")
			}
		})
	}
}
