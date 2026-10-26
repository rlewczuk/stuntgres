package analyze

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineProcedureInvocationSchema(t *testing.T) {
	u := make(map[sqltype.ID]sqltype.Usage)
	for _, typ := range sqltype.Builtins() {
		u[typ.ID()] = typ.Usage()
	}
	for _, tc := range []struct {
		name           string
		modes          []string
		pseudo, actual sqltype.ID
	}{
		{"sole", []string{"i", "o"}, 2283, sqltype.Int4},
		{"multiple", []string{"i", "o", "o"}, 2283, sqltype.Text},
		{"inout", []string{"b", "o"}, 2283, sqltype.Int8},
		{"array", []string{"i", "o"}, 2277, 1007},
		{"array_inout", []string{"b", "o"}, 2277, 1007},
		{"no_output", []string{"i"}, 2283, sqltype.Int4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &proc.Definition{Procedure: true}
			formals := make([]proc.Formal, len(tc.modes))
			for i, mode := range tc.modes {
				formals[i] = proc.Formal{Mode: mode, Name: []string{"x", "answer", "second"}[i], Usage: u[tc.pseudo]}
			}
			if err := d.SetFormals(formals); err != nil {
				t.Fatal(err)
			}
			if len(d.OutputFormals) == 0 {
				d.Result = u[2278]
			} else {
				tuple, err := d.OutputTuple()
				if err != nil {
					t.Fatal(err)
				}
				d.Result = tuple.Usage()
			}
			result, err := d.ResolveResult(func(formal sqltype.Usage) sqltype.Usage {
				if sqltype.PolymorphicFamily(formal.Type.ID()) != 0 {
					return u[tc.actual]
				}
				return formal
			})
			if err != nil {
				t.Fatal(err)
			}
			signature := proc.InvocationSignature{Inputs: []sqltype.Usage{u[tc.actual]}, Result: result}
			if !signature.ValidFor(d) {
				t.Fatal("fixture invocation is not checked")
			}
			procedure := &catalog.Procedure{RoutineKind: catalog.ProcedureRoutine, SQL: d}
			call := plan.SQLRoutineExpr{Info: expr.Info{Usage: result, RoutineReference: procedure}, Procedure: procedure, Signature: signature}
			binding := Binding{Expressions: [][]expr.Expr{{call}}}
			original := slices.Clone(d.Formals)
			columns := RoutineCallSchema(binding)
			if len(columns) != len(d.OutputFormals) {
				t.Fatal("wrong CALL width", columns)
			}
			for i, column := range columns {
				member := signature.Result.Type.CompositeMember(i)
				if column.Name != d.Result.Type.CompositeMember(i).Name || column.Name != member.Name || column.Usage != member.Usage || column.Usage != u[tc.actual] || !column.Usage.Valid() {
					t.Fatal("CALL lost exact invocation member", i, column)
				}
			}
			if len(columns) != 0 {
				columns[0].Name = "caller"
				columns[0].Usage = u[sqltype.Bytea]
			}
			again := RoutineCallSchema(binding)
			if len(again) != 0 && (again[0].Name == "caller" || again[0].Usage != result.Type.CompositeMember(0).Usage) {
				t.Fatal("schema borrowed mutable backing")
			}
			retained := binding.Expressions[0][0].(plan.SQLRoutineExpr)
			if retained.Signature.Result != result || retained.Usage != result || retained.Procedure != procedure || !slices.Equal(d.Formals, original) || d.Result == result && len(d.OutputFormals) != 0 {
				t.Fatal("schema changed checked binding or declaration")
			}
		})
	}
}
