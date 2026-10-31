package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineScalarRecordSignature(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION record_signature() RETURNS record LANGUAGE SQL AS $$SELECT 1 AS a,'x'::text AS b$$")
	enginetest.Run(t, e, "CREATE FUNCTION record_signature_pl(x record) RETURNS record LANGUAGE plpgsql AS $$BEGIN RETURN x; END$$")
	for _, query := range []string{
		"SELECT record_signature()",
		"SELECT record_signature_pl(ROW(1,'x'::text))",
		"SELECT record_signature_pl(record_signature_pl(ROW(1,'x'::text)))",
	} {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, query).(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal("accepted call failed before invocation", query, err)
		}
		call := node.(plan.SQLRoutineExpr)
		declaration := call.Procedure.SQL
		owned, _ := e.CatalogRead().LookupType(2249)
		if declaration.Result.Type != owned || !call.Usage.Type.IsDeferredRecord() || call.Usage.Type.IsComposite() ||
			call.Signature.Result != call.Usage || !call.Signature.ValidFor(declaration) {
			t.Fatal("call did not separate declaration from deferred transport", query)
		}
		if err := call.ValidateStoredProvider(); err != nil {
			t.Fatal("accepted call lost stored provider provenance", err)
		}
		private, err := declaration.Specialize(call.Signature)
		if err != nil || private == declaration || private.Result != call.Usage || declaration.Result.Type != owned {
			t.Fatal("private invocation changed the declaration", err)
		}
		if _, err := b.CompleteFunctionDefinitions(raw.(parser.FunctionExpr), call, nil); err == nil {
			t.Fatal("generic scalar RECORD bypassed caller definitions")
		}
		definitions := []parser.FunctionColumnDefinition{
			{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "integer"}},
			{Name: parser.Identifier{Text: "b"}, Type: parser.TypeName{Name: "text"}},
		}
		completed, err := b.CompleteFunctionDefinitions(raw.(parser.FunctionExpr), call, definitions)
		if err != nil {
			t.Fatal(err)
		}
		shaped := completed.(plan.SQLRoutineExpr)
		if !shaped.Usage.Type.IsComposite() || shaped.Procedure != call.Procedure || shaped.RoutineReference != call.Procedure ||
			shaped.Ref != call.Ref || !shaped.Signature.ValidFor(declaration) || shaped.Signature.Result != shaped.Usage {
			t.Fatal("FROM completion lost accepted signature provenance")
		}
		if err := shaped.ValidateStoredProvider(); err != nil {
			t.Fatal("completed call rejected before producer", err)
		}
		if _, err := declaration.Specialize(shaped.Signature); err != nil {
			t.Fatal("completed invocation rejected its private result", err)
		}
		foreign, _ := engine.New().CatalogRead().LookupType(2249)
		bad := call
		bad.Usage, bad.Signature = foreign.Usage(), proc.InvocationSignature{Inputs: call.Signature.Inputs, Result: foreign.Usage()}
		if err := bad.ValidateStoredProvider(); err == nil {
			t.Fatal("foreign same-OID declaration acquired invocation authority")
		}
	}
}
