package pgcatalog

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestAggregateOrderedProcedureVariadic(t *testing.T) {
	var integer *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 { integer = typ }
	}
	array, err := sqltype.Array(1007, "_int4", integer)
	if err != nil { t.Fatal(err) }
	for _, tc := range []struct {
		name string
		procedure catalog.Procedure
		want ids.OID
	}{
		{"plain", catalog.Procedure{Signature: "23,25"}, 0},
		{"builtin-any", catalog.Procedure{Builtin: &catalog.BuiltinRoutineDeclaration{Variadic: 2276}}, 2276},
		{"builtin-array", catalog.Procedure{Builtin: &catalog.BuiltinRoutineDeclaration{Variadic: 23}}, 23},
		{"owned-any", catalog.Procedure{Signature: "2276", Aggregate: &catalog.OwnedAggregate{Variadic: true}}, 2276},
		{"owned-fixed", catalog.Procedure{Signature: "23,23", Aggregate: &catalog.OwnedAggregate{}}, 0},
		{"sql-array", catalog.Procedure{SQL: &proc.Definition{Formals: []proc.Formal{{Mode: "i", Usage: integer.Usage()}, {Mode: "v", Usage: array.Usage()}, {Mode: "o", Usage: integer.Usage()}}}}, 23},
		{"sql-fixed", catalog.Procedure{SQL: &proc.Definition{Formals: []proc.Formal{{Mode: "i", Usage: integer.Usage()}}}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProcedureVariadic(&tc.procedure)
			if err != nil || got != tc.want { t.Fatal(got, err, tc.want) }
		})
	}
	if got := AttributeNumber(ids.ProcedureClassID, "provariadic", 16); got != 8 { t.Fatal("released attribute number", got) }
	malformed := &catalog.Procedure{SQL: &proc.Definition{Formals: []proc.Formal{{Mode: "v", Usage: integer.Usage()}}}}
	if _, err := ProcedureVariadic(malformed); err == nil { t.Fatal("nonarray variadic formal accepted") }
}
