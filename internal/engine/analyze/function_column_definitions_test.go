package analyze

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRoutineResultDefinitionCompletion(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	binder := new(Binder)
	raw := parser.FunctionExpr{Pos: parser.Position{Character: 15}}
	definitions := []parser.FunctionColumnDefinition{{Name: parser.Identifier{Text: "replacement"}}}
	for _, mode := range []string{"o", "t"} {
		for _, tc := range []struct{ input, output, actual sqltype.ID }{
			{2283, 3500, sqltype.Int4},
			{2283, 2776, 1007},
			{5077, 5079, 1007},
		} {
			declaration := &proc.Definition{ReturnsSet: mode == "t"}
			if err := declaration.SetFormals([]proc.Formal{
				{Mode: "i", Name: "x", Usage: types[tc.input].Usage()},
				{Mode: mode, Name: "a", Usage: types[tc.output].Usage()},
				{Mode: mode, Name: "b", Usage: types[sqltype.Int4].Usage()},
			}); err != nil {
				t.Fatal(err)
			}
			tuple, err := declaration.OutputTuple()
			if err != nil {
				t.Fatal(err)
			}
			declaration.Result = tuple.Usage()
			result, err := declaration.ResolveResult(func(formal sqltype.Usage) sqltype.Usage {
				if sqltype.PolymorphicFamily(formal.Type.ID()) != 0 {
					return types[tc.actual].Usage()
				}
				return formal
			})
			if err != nil {
				t.Fatal(err)
			}
			signature := proc.InvocationSignature{Inputs: []sqltype.Usage{types[tc.actual].Usage()}, Result: result}
			private, err := declaration.Specialize(signature)
			if err != nil {
				t.Fatal(err)
			}
			procedure := &catalog.Procedure{
				ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000},
				SQL: declaration, Result: 2249,
			}
			domain := new(ids.Domain)
			call := plan.SQLRoutineExpr{
				Info: expr.Info{Usage: result, RoutineReference: procedure}, Procedure: procedure,
				Ref: domain.Ref(1, procedure.Address()), Signature: signature,
			}
			completed, err := binder.CompleteFunctionDefinitions(raw, call, nil)
			if err != nil {
				t.Fatal("formal-derived shape demanded caller definitions", mode, tc, err)
			}
			got, ok := completed.(plan.SQLRoutineExpr)
			if !ok || got.Procedure != procedure || got.RoutineReference != procedure || got.Ref != call.Ref ||
				got.Usage != result || !got.Signature.Equal(signature) || !got.Signature.ValidFor(declaration) ||
				!private.MatchesOutputTuple(got.Usage.Type) || declaration.Result.Type != tuple ||
				declaration.Formals[1].Usage != types[tc.output].Usage() {
				t.Fatal("completion replaced declaration provenance or immutable specialization", mode, tc)
			}
			_, err = binder.CompleteFunctionDefinitions(raw, call, definitions)
			diagnostic, ok := err.(*sqlstate.Error)
			if !ok || diagnostic.Code != "42601" || diagnostic.Message != "a column definition list is redundant for a function with OUT parameters" {
				t.Fatal("caller definitions replaced formal-derived shape", mode, tc, err)
			}
		}
	}
	scalar := &catalog.Procedure{SQL: &proc.Definition{Result: sqltype.DeferredRecord().Usage()}, Result: 2249}
	call := plan.SQLRoutineExpr{Info: expr.Info{Usage: scalar.SQL.Result}, Procedure: scalar}
	_, err := binder.CompleteFunctionDefinitions(raw, call, nil)
	diagnostic, ok := err.(*sqlstate.Error)
	if !ok || diagnostic.Code != "42601" || diagnostic.Message != `a column definition list is required for functions returning "record"` || diagnostic.Position != "15" {
		t.Fatal("scalar RECORD lost its caller-definition requirement", err)
	}
}
