package plan

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineStoredContainerAuthority(t *testing.T) {
	types, foreign := make(map[sqltype.ID]sqltype.Usage), make(map[sqltype.ID]sqltype.Usage)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ.Usage()
	}
	for _, typ := range sqltype.Builtins() {
		foreign[typ.ID()] = typ.Usage()
	}
	array, ranged, multi := *types[1007].Type, *types[3904].Type, *types[4451].Type
	for _, family := range []struct {
		name string
		scalar, array, ranged, multi sqltype.ID
	}{
		{"simple", 2283, 2277, 3831, 4537},
		{"common", 5077, 5078, 5080, 4538},
	} {
		t.Run(family.name, func(t *testing.T) {
			for _, tc := range []struct {
				name string
				formals []sqltype.ID
				inputs []sqltype.Usage
				resultFormal sqltype.ID
				result sqltype.Usage
				valid bool
			}{
				{"scalar", []sqltype.ID{family.scalar}, []sqltype.Usage{types[sqltype.Int4]}, family.scalar, types[sqltype.Int4], true},
				{"array", []sqltype.ID{family.array}, []sqltype.Usage{types[1007]}, family.array, types[1007], true},
				{"range", []sqltype.ID{family.ranged, family.scalar}, []sqltype.Usage{types[3904], types[sqltype.Int4]}, family.multi, types[4451], true},
				{"multirange", []sqltype.ID{family.multi}, []sqltype.Usage{types[4451]}, family.ranged, types[3904], true},
				{"copied-array-result", []sqltype.ID{family.array}, []sqltype.Usage{types[1007]}, family.array, array.Usage(), false},
				{"mismatched-array-inputs", []sqltype.ID{family.array, family.array}, []sqltype.Usage{types[1007], array.Usage()}, family.scalar, types[sqltype.Int4], false},
				{"foreign-range-subtype", []sqltype.ID{family.ranged, family.scalar}, []sqltype.Usage{types[3904], foreign[sqltype.Int4]}, family.ranged, types[3904], false},
				{"foreign-multirange-subtype", []sqltype.ID{family.multi}, []sqltype.Usage{types[4451]}, family.scalar, foreign[sqltype.Int4], false},
				{"copied-range-companion", []sqltype.ID{family.ranged}, []sqltype.Usage{ranged.Usage()}, family.multi, types[4451], false},
				{"copied-multirange-companion", []sqltype.ID{family.ranged}, []sqltype.Usage{types[3904]}, family.multi, multi.Usage(), false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					definition := &proc.Definition{Result: types[tc.resultFormal]}
					formals := make([]proc.Formal, len(tc.formals))
					for i, id := range tc.formals {
						formals[i] = proc.Formal{Mode: "i", Usage: types[id]}
					}
					if err := definition.SetFormals(formals); err != nil {
						t.Fatal(err)
					}
					procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000}, SQL: definition, Result: ids.OID(tc.resultFormal)}
					domain := new(ids.Domain)
					call := SQLRoutineExpr{
						Info: expr.Info{Usage: tc.result, RoutineReference: procedure},
						Procedure: procedure, Ref: domain.Ref(1, procedure.Address()),
						WrittenCount: len(tc.inputs),
						Signature: proc.InvocationSignature{Inputs: tc.inputs, Result: tc.result},
					}
					for i, usage := range tc.inputs {
						call.Args = append(call.Args, LiteralExpr{Info: expr.Info{Usage: usage}, Value: sqltype.Null(usage.Type)})
						call.InputMap = append(call.InputMap, RoutineInput{Start: i, Count: 1})
						call.ChildSlots = append(call.ChildSlots, i)
					}
					call.Property = call.GroupProperties(call.Args)
					if err := call.ValidateStoredProvider(); (err == nil) != tc.valid {
						t.Fatalf("stored provider validation = %v, want valid %v", err, tc.valid)
					}
				})
			}
		})
	}
}
