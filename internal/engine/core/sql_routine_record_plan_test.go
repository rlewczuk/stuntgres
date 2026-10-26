package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineDeclaredRecordPlan(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	record, _ := read.LookupType(2249)
	integer, _ := read.LookupType(sqltype.Int4)
	text, _ := read.LookupType(sqltype.Text)
	one, _ := sqltype.Integer(integer, 1)
	columns := []catalog.Column{{Name: "a", Usage: integer.Usage()}, {Name: "b", Usage: text.Usage()}}
	binding := analyze.Binding{Query: &plan.Query{Root: &plan.ProjectNode{QuerySchema: columns}}}
	auth := core.AuthorizationView{View: access.View{Catalog: read}}
	conversion, err := core.ValidateSQLRoutineTerminal(&parser.Select{}, binding, record.Usage(), auth, nil)
	if err != nil {
		t.Fatal(err)
	}
	value, err := conversion.Apply(context.Background(), []sqltype.Value{one, sqltype.String(text, "x")}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	actual := value.ActualRecord().Type()
	if !value.Type().IsDeferredRecord() || actual.CompositeLen() != 2 || actual.CompositeMember(0).Name != "a" || actual.CompositeMember(1).Name != "b" {
		t.Fatal("declaration did not produce an owned, labelled record", value)
	}
	if record.IsDeferredRecord() || record.IsComposite() {
		t.Fatal("planning changed the catalog declaration")
	}
	foreign, _ := engine.New().CatalogRead().LookupType(2249)
	if _, err := core.ValidateSQLRoutineTerminal(&parser.Select{}, binding, foreign.Usage(), auth, nil); err == nil {
		t.Fatal("foreign declaration acquired executable record authority")
	}
	if _, err := conversion.Apply(context.Background(), []sqltype.Value{one}, sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}); err == nil {
		t.Fatal("packed record accepted a truncated row")
	}
}

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
