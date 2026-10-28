package core

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestFDWSQLValidatorFrame(t *testing.T) {
	state := catalog.NewState(1)
	catalog.InstallBuiltinTypes(state, sqltype.Builtins(), authorization.PrincipalRef{})
	read := catalog.NewRead(&catalog.Owner{}, state)
	array, _ := read.LookupType(1009)
	oid, _ := read.LookupType(sqltype.Oid)
	integer, _ := read.LookupType(sqltype.Int4)
	d := &catalog.Procedure{SQL: &proc.Definition{Inputs: []sqltype.Usage{array.Usage(), oid.Usage()}, Result: integer.Usage()}}
	for _, kind := range []catalog.ForeignObjectKind{catalog.ForeignWrapper, catalog.ForeignServer, catalog.ForeignUserMapping, catalog.ForeignTable, catalog.ForeignAttribute} {
		for _, options := range [][]catalog.ForeignOption{nil, {{Name: "b", Value: "quote'=雪"}, {Name: "a", Value: ""}}} {
			frame, result, err := foreignValidatorFrame(read, d, kind, options)
			if err != nil {
				t.Fatal(err)
			}
			if result != integer.Usage() || frame.Inputs[0] != array.Usage() || frame.Inputs[1] != oid.Usage() || frame.Parameters[0].IsNull() || frame.Parameters[0].ArrayLen() != len(options) {
				t.Fatal("typed non-NULL option frame", frame, result)
			}
			class, err := frame.Parameters[1].OID()
			if err != nil || class != uint32(kind) {
				t.Fatal("catalog class argument", class, kind, err)
			}
			if len(options) == 0 && frame.Parameters[0].ArrayRank() != 0 {
				t.Fatal("empty array rank")
			}
			if len(options) != 0 {
				options[0].Value = "mutated"
				first, firstErr := frame.Parameters[0].ArrayElement(0).Text()
				second, secondErr := frame.Parameters[0].ArrayElement(1).Text()
				if firstErr != nil || secondErr != nil || first != "b=quote'=雪" || second != "a=" {
					t.Fatal("ordered owned option bytes")
				}
			}
			frame.Close()
			if frame.Parameters != nil {
				t.Fatal("closed frame retained arguments")
			}
		}
	}
	if frame, _, err := foreignValidatorFrame(read, &catalog.Procedure{}, catalog.ForeignServer, nil); err == nil || frame != nil {
		t.Fatal("unavailable routine acquired a frame", frame, err)
	}
}
