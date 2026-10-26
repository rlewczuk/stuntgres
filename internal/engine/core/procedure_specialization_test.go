package core_test

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLProcedureInvocationSpecializationComposed(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, `CREATE PROCEDURE procedure_projection(IN x anyelement, OUT answer anyelement, OUT other anyelement) LANGUAGE plpgsql AS 'DECLARE "copy" answer%TYPE; transitive "copy"%TYPE; BEGIN RETURN; END'`)
	var published *catalog.Procedure
	for _, source := range []string{"CALL procedure_projection(7,NULL,NULL)", "CALL procedure_projection('word'::text,NULL,NULL)", "CALL procedure_projection(8,NULL,NULL)"} {
		ast, description := sequencePreparedDescription(t, e, s, source)
		op, err := s.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		func() {
			defer op.Done()
			defer scope.Finish(nil)
			if err := scope.Gate(ast); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			defer e.Mu.Unlock()
			auth, err := scope.RoutineAuthorizationLocked()
			if err != nil {
				t.Fatal(err)
			}
			binding, err := analyze.Describe(op.Context(), e.AnalysisHost(op), ast, core.AuthorityOf(auth), ids.ObjectRef{}, sqltype.InputEnvironment{}, analyze.Expected{}, nil, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			call := binding.Expressions[0][0].(plan.SQLRoutineExpr)
			if !call.Signature.ValidFor(call.Procedure.SQL) || call.Usage != call.Signature.Result {
				t.Fatal("binding lost checked invocation")
			}
			if published == nil {
				published = call.Procedure
			} else if published != call.Procedure {
				t.Fatal("invocation republished declaration")
			}
			original := slices.Clone(published.SQL.Formals)
			program := published.SQL.PL
			definition, err := published.SQL.Specialize(call.Signature)
			if err != nil {
				t.Fatal(err)
			}
			private := *published
			private.SQL = definition
			if err := e.CompilePLRoutineLocked(op, auth, &private); err != nil {
				t.Fatal(err)
			}
			if private.SQL == published.SQL || private.SQL.PL == program || &private.SQL.Formals[0] == &published.SQL.Formals[0] || private.SQL.PLSyntax != published.SQL.PLSyntax {
				t.Fatal("private compilation ownership")
			}
			if err := private.SQL.PL.ValidateDefinition(private.SQL); err != nil {
				t.Fatal(err)
			}
			body := private.SQL.PL.Root.Statements[0].Block
			for i, index := range definition.OutputFormals {
				member := call.Signature.Result.Type.CompositeMember(i)
				if private.SQL.Formals[index].Usage != member.Usage || private.SQL.PL.Variables[index].Usage != member.Usage || description.Results[i].Usage != member.Usage || description.Results[i].Name != member.Name {
					t.Fatal("compiled OUT/Describe does not use invocation member", i)
				}
			}
			for _, name := range []string{"copy", "transitive"} {
				if private.SQL.PL.Variables[body.Bindings[name]].Usage != call.Signature.Result.Type.CompositeMember(0).Usage {
					t.Fatal("transitive TYPE copy lost concrete usage", name)
				}
			}
			if published.SQL.PL != program || !slices.Equal(published.SQL.Formals, original) || published.SQL.Formals[1].Usage.Type.ID() != 2283 {
				t.Fatal("compilation changed published declaration")
			}
		}()
	}
}
