package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

// Scalar transport is shared by B-tree keys and HASH scalars. Containers here
// exercise recursive payload remapping, not new compound index providers.
func TestDomainCheckIndexSnapshotAliasRecursive(t *testing.T) {
	for _, routineID := range []sqltype.ID{sqltype.Regproc, sqltype.Regprocedure} {
		t.Run(aclUnitType(t, New(), routineID).Name(), func(t *testing.T) {
			source, target := New(), New()
			fromRoutine, toRoutine := aclUnitType(t, source, routineID), aclUnitType(t, target, routineID)
			fromDictionary, toDictionary := aclUnitType(t, source, sqltype.Regdictionary), aclUnitType(t, target, sqltype.Regdictionary)
			fromConfig, toConfig := aclUnitType(t, source, sqltype.Regconfig), aclUnitType(t, target, sqltype.Regconfig)
			domain := func(id sqltype.ID, name string, base *sqltype.Descriptor) *sqltype.Descriptor {
				t.Helper()
				d, err := sqltype.Domain(id, name, name, base.Usage())
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			fromRoutineDomain := domain(45000, "snapshot_routine", fromRoutine)
			toRoutineDomain := domain(45000, "snapshot_routine", toRoutine)
			fromDictionaryDomain := domain(45001, "snapshot_dictionary", fromDictionary)
			toDictionaryDomain := domain(45001, "snapshot_dictionary", toDictionary)
			fromArray, err := sqltype.Array(45002, "snapshot_routines", fromRoutineDomain)
			if err != nil {
				t.Fatal(err)
			}
			toArray, err := sqltype.Array(45002, "snapshot_routines", toRoutineDomain)
			if err != nil {
				t.Fatal(err)
			}
			fromArrayDomain := domain(45003, "snapshot_array", fromArray)
			toArrayDomain := domain(45003, "snapshot_array", toArray)
			wrap := func(d *sqltype.Descriptor, v sqltype.Value) sqltype.Value {
				t.Helper()
				v, err := d.Wrap(v)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			routine, err := sqltype.RegprocValue(fromRoutine, 41000)
			if err != nil {
				t.Fatal(err)
			}
			fixed, err := sqltype.RegprocValue(fromRoutine, 1397)
			if err != nil {
				t.Fatal(err)
			}
			dictionary, err := sqltype.RegTSValue(fromDictionary, 41000)
			if err != nil {
				t.Fatal(err)
			}
			config, err := sqltype.RegTSValue(fromConfig, 41000)
			if err != nil {
				t.Fatal(err)
			}
			array, err := sqltype.OwnedArrayValue(fromArray, []sqltype.ArrayDimension{{Lower: -1, Length: 3}}, []sqltype.Value{wrap(fromRoutineDomain, routine), wrap(fromRoutineDomain, fixed), sqltype.Null(fromRoutineDomain)})
			if err != nil {
				t.Fatal(err)
			}
			composite := func(arrayDomain, dictionaryDomain, configType *sqltype.Descriptor) *sqltype.Descriptor {
				t.Helper()
				d, err := sqltype.Composite(45004, "snapshot_alias_record", "snapshot_alias_record", 0, []sqltype.CompositeMember{
					{Attribute: 1, Name: "routines", Usage: arrayDomain.Usage()},
					{Attribute: 2, Name: "dictionary", Usage: dictionaryDomain.Usage()},
					{Attribute: 3, Name: "configuration", Usage: configType.Usage()},
					{Attribute: 4, Name: "null_dictionary", Usage: dictionaryDomain.Usage()},
				})
				if err != nil {
					t.Fatal(err)
				}
				return d
			}
			fromComposite := composite(fromArrayDomain, fromDictionaryDomain, fromConfig)
			toComposite := composite(toArrayDomain, toDictionaryDomain, toConfig)
			original, err := sqltype.OwnedCompositeValue(fromComposite, []sqltype.Value{wrap(fromArrayDomain, array), wrap(fromDictionaryDomain, dictionary), config, sqltype.Null(fromDictionaryDomain)})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := &indexSnapshot{mapping: map[ObjectAddress]ObjectAddress{
				{ClassID: ProcedureClassID, ObjectID: 41000}:    {ClassID: ProcedureClassID, ObjectID: 42000},
				{ClassID: TSDictionaryClassID, ObjectID: 41000}: {ClassID: TSDictionaryClassID, ObjectID: 43000},
				{ClassID: RelationClassID, ObjectID: 41000}:     {ClassID: RelationClassID, ObjectID: 44000},
				{ClassID: TypeClassID, ObjectID: 1397}:          {ClassID: TypeClassID, ObjectID: 45005},
			}}
			mapped, err := snapshot.datum(original, toComposite, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			nested, err := mapped.CompositeField(0).BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			mappedRoutine, err := nested.ArrayElement(0).ProcedureOID()
			if err != nil || mappedRoutine != 42000 {
				t.Fatal("routine key did not follow procedure-class identity", mappedRoutine, err)
			}
			mappedDictionary, err := mapped.CompositeField(1).RegTSOID()
			if err != nil || mappedDictionary != 43000 {
				t.Fatal("dictionary key did not follow dictionary-class identity", mappedDictionary, err)
			}
			fixedRoutine, err := nested.ArrayElement(1).ProcedureOID()
			if err != nil || fixedRoutine != 1397 {
				t.Fatal("source-fixed routine adopted a different catalog class", fixedRoutine, err)
			}
			fixedConfig, err := mapped.CompositeField(2).RegTSOID()
			if err != nil || fixedConfig != 41000 {
				t.Fatal("configuration adopted dictionary identity", fixedConfig, err)
			}
			if mapped.Type() != toComposite || mapped.CompositeField(0).Type() != toArrayDomain || nested.Type() != toArray || nested.ArrayElement(0).Type() != toRoutineDomain || mapped.CompositeField(1).Type() != toDictionaryDomain || !nested.ArrayElement(2).IsNull() || !mapped.CompositeField(3).IsNull() || nested.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: -1, Length: 3}) {
				t.Fatal("recursive transport lost destination generations, bounds or NULLs")
			}
			originalRoutine, err := array.ArrayElement(0).ProcedureOID()
			if err != nil || originalRoutine != 41000 {
				t.Fatal("transport mutated source routine", originalRoutine, err)
			}
			originalDictionary, err := original.CompositeField(1).RegTSOID()
			if err != nil || originalDictionary != 41000 || original.Type() != fromComposite || original.CompositeField(0).Type() != fromArrayDomain || array.Type() != fromArray {
				t.Fatal("transport mutated source container or dictionary", originalDictionary, err)
			}
			null, err := snapshot.datum(sqltype.Null(fromComposite), toComposite, nil, nil)
			if err != nil || !null.IsNull() || null.Type() != toComposite {
				t.Fatal("transport lost outer NULL destination identity", err)
			}
		})
	}
}

func TestDomainCheckIndexSnapshotAliasKeyOutput(t *testing.T) {
	e := New()
	catalog := e.CatalogRead()
	class, ok := sqltype.LookupIndexClass("oid_ops")
	if !ok {
		t.Fatal("missing installed OID class")
	}
	for _, test := range []struct {
		id   sqltype.ID
		want string
	}{
		{sqltype.Regproc, "41000"},
		{sqltype.Regprocedure, "41000"},
		{sqltype.Regdictionary, "41000"},
		{sqltype.Int4, "4294967295"},
	} {
		base := aclUnitType(t, e, test.id)
		t.Run(base.Name(), func(t *testing.T) {
			var value sqltype.Value
			var err error
			switch test.id {
			case sqltype.Int4:
				value, err = sqltype.Integer(base, -1)
			case sqltype.Regdictionary:
				value, err = sqltype.RegTSValue(base, 41000)
			default:
				value, err = sqltype.RegprocValue(base, 41000)
			}
			if err != nil {
				t.Fatal(err)
			}
			domain, err := sqltype.Domain(45000, "diagnostic_alias", "diagnostic_alias", base.Usage())
			if err != nil {
				t.Fatal(err)
			}
			wrapped, err := domain.Wrap(value)
			if err != nil {
				t.Fatal(err)
			}
			text, err := uniqueKeyOutput(context.Background(), wrapped, class, catalog, sqltype.OutputEnvironment{})
			if err != nil || text != test.want {
				t.Fatal("physical OID key output", text, err)
			}
			physical, err := wrapped.BaseValue()
			if err != nil || physical != value || wrapped.Type() != domain {
				t.Fatal("diagnostic output changed logical source key", err)
			}
		})
	}
}
