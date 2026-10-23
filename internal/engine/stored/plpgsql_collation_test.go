package stored

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func plpgsqlLocalUsageFixture(t *testing.T) (catalog.Read, *catalog.Procedure, []*sqltype.Descriptor, []*catalog.Collation) {
	t.Helper()
	read, relation, integer, _ := routineCaptureFixture(t)
	varchar, ok := read.LookupType(sqltype.VarChar)
	if !ok {
		t.Fatal("missing varchar")
	}
	var unicode catalog.Collation
	for _, seed := range catalog.BuiltinCollations {
		descriptor := seed
		read.State().SetDescriptor(descriptor.Address(), &descriptor)
		if seed.Name() == "pg_unicode_fast" {
			unicode = seed
		}
	}
	if unicode.OID == 0 {
		t.Fatal("missing real Unicode collation provider")
	}
	types := make([]*sqltype.Descriptor, 3)
	collations := make([]*catalog.Collation, 3)
	for i, name := range []string{"source", "destination", "second"} {
		typ, err := sqltype.Domain(sqltype.ID(96000+i), name+"_text", name+"_text", varchar.Usage())
		if err != nil {
			t.Fatal(err)
		}
		types[i] = typ
		descriptor := &catalog.Type{SQLType: typ, ObjectNamespace: relation.Namespace(), ObjectRevision: 1}
		read.State().SetDescriptor(descriptor.Address(), descriptor)
		collation := unicode
		collation.OID = ids.OID(97000 + i)
		collation.ObjectName = name + "_unicode"
		collation.ObjectNamespace = relation.Namespace()
		collations[i] = &collation
		read.State().SetDescriptor(collation.Address(), &collation)
	}
	usage := types[0].Usage()
	usage.Typmod = 16
	var err error
	usage.Collation, err = collations[0].Usage()
	if err != nil {
		t.Fatal(err)
	}
	definition := &proc.Definition{Kind: proc.PLpgSQLBody, Name: "copied_usage", Source: "DECLARE x local_type; BEGIN RETURN 1; END", Result: integer.Usage()}
	raw, err := parser.ParsePLpgSQL(definition.Source)
	if err != nil {
		t.Fatal(err)
	}
	definition.PLSyntax = raw
	definition.PL, err = proc.CompilePL(definition, raw, false, func(parser.PLType) (sqltype.Usage, bool, error) { return usage, false, nil })
	if err != nil {
		t.Fatal(err)
	}
	source := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000}, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, ObjectName: definition.Name, Owner: read.Owner().Principals.Principal(10), Result: ids.OID(integer.ID()), SQL: definition}
	read.State().SetDescriptor(source.Address(), source)
	read.State().BindName(catalog.DescriptorNameKey(source), source.Address())
	return read, source, types, collations
}

func TestPLpgSQLLocalUsageReplacement(t *testing.T) {
	for _, remapped := range []bool{false, true} {
		name := "empty_map"
		if remapped {
			name = "nonidentity_map"
		}
		t.Run(name, func(t *testing.T) {
			read, source, types, collations := plpgsqlLocalUsageFixture(t)
			original := source.SQL.PL.Variables[0].Usage
			want := original
			mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
			if remapped {
				mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[0].ID())}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())}
				mapping[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[1].ID())}] = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(types[2].ID())}
				mapping[collations[0].Address()] = collations[1].Address()
				mapping[collations[1].Address()] = collations[2].Address()
				want.Type = types[1]
				var err error
				want.Collation, err = collations[1].Usage()
				if err != nil {
					t.Fatal(err)
				}
			}
			typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
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
			remap := NewRoutineRemap(read, mapping, typ, nil, false)
			if err := routine.RebindSQLDefinitions(read.State(), remap); err != nil {
				t.Fatal(err)
			}
			next, ok := read.Procedure(source.Address())
			if !ok {
				t.Fatal("replacement lost procedure")
			}
			if got := next.SQL.PL.Variables[0].Usage; got != want {
				t.Fatalf("local Usage after replacement %#v, want %#v", got, want)
			}
			if source.SQL.PL.Variables[0].Usage != original || next.SQL.PL.Variables[0].Usage.Collation.CaseMode() != original.Collation.CaseMode() {
				t.Fatal("replacement mutated source/shared program or changed collation execution semantics")
			}
		})
	}
	t.Run("incompatible_collation", func(t *testing.T) {
		read, source, _, collations := plpgsqlLocalUsageFixture(t)
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{collations[0].Address(): {ClassID: ids.CollationClassID, ObjectID: 950}}
		remap := NewRoutineRemap(read, mapping, func(old *sqltype.Descriptor) *sqltype.Descriptor { return old }, nil, false)
		if err := routine.RebindSQLDefinitions(read.State(), remap); err == nil {
			t.Fatal("incompatible real collation provider accepted")
		}
		if current, ok := read.Procedure(source.Address()); !ok || current != source {
			t.Fatal("failed collation import published a routine candidate")
		}
	})
}

func TestPLpgSQLLocalUsageEquivalence(t *testing.T) {
	read, source, types, collations := plpgsqlLocalUsageFixture(t)
	identity := func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ }
	current := *source
	definition := *source.SQL
	program := *source.SQL.PL
	program.Variables = append([]proc.PLVariable(nil), program.Variables...)
	definition.PL = &program
	current.SQL = &definition
	if same, err := sameMappedSQLRoutine(&current, source, identity, nil); err != nil || !same {
		t.Fatal("equivalent private Usage copy rejected", err)
	}
	text, ok := read.LookupType(sqltype.Text)
	if !ok {
		t.Fatal("missing text")
	}
	program.Variables[0].Usage.Collation = text.Usage().Collation
	if same, err := sameMappedSQLRoutine(&current, source, identity, nil); err != nil || same {
		t.Fatal("collation-only local semantic change accepted as equivalent", err)
	}
	if source.SQL.PL.Variables[0].Usage.Collation == program.Variables[0].Usage.Collation {
		t.Fatal("equivalence fixture mutated original lexical usage")
	}
	program.Variables[0].Usage = source.SQL.PL.Variables[0].Usage
	program.Variables[0].Usage.Type = types[1]
	var err error
	program.Variables[0].Usage.Collation, err = collations[1].Usage()
	if err != nil {
		t.Fatal(err)
	}
	mappedType := func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == types[0] {
			return types[1]
		}
		return typ
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{collations[0].Address(): collations[1].Address(), collations[1].Address(): collations[2].Address()}
	if same, err := sameMappedSQLRoutine(&current, source, mappedType, typedef.RemappedCollationCapabilities(read.State(), mapping)); err != nil || !same {
		t.Fatal("once-mapped equivalent collation rejected", err)
	}
	incompatible := map[ids.ObjectAddress]ids.ObjectAddress{collations[0].Address(): {ClassID: ids.CollationClassID, ObjectID: 950}}
	if same, err := sameMappedSQLRoutine(&current, source, mappedType, typedef.RemappedCollationCapabilities(read.State(), incompatible)); err == nil || same {
		t.Fatal("equivalence swallowed incompatible collation capability", err)
	}
}
