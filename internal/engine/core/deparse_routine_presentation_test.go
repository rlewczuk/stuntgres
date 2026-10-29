package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestRoutineDeparsePresentation(t *testing.T) {
	e := engine.New()
	enginetest.Run(t,e,"CREATE FUNCTION rd_f(a integer DEFAULT 7) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
	enginetest.Run(t,e,"CREATE FUNCTION rd_atomic() RETURNS integer BEGIN ATOMIC SELECT 7; RETURN 8; END")
	enginetest.Run(t,e,"CREATE FUNCTION rd_alias() RETURNS integer BEGIN ATOMIC SELECT 7 AS value; END")
	enginetest.Run(t,e,"CREATE TABLE rd_relation_source(id integer)")
	enginetest.Run(t,e,"CREATE FUNCTION rd_relation() RETURNS integer BEGIN ATOMIC SELECT id FROM rd_relation_source; END")
	read := e.CatalogRead()
	public := read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"public"})
	path := []ids.ObjectAddress{{ClassID:ids.NamespaceClassID,ObjectID:ids.PGCatalogNamespaceOID},public}
	scope := &core.StatementScope{Inquiry:core.NewInquiry(core.InquiryFields{CommentCatalog:read,DeparseAuth:catalogAuthorization(e,read),DeparsePath:path})}
	ctx := &exec.Context{EvalStatement:exec.EvalStatement{Context:context.Background(),Host:core.HostOf(scope)}}
	oidType,_ := read.LookupType(sqltype.Oid)
	textType,_ := read.LookupType(sqltype.Text)
	inquiry := func(id ids.OID,signature functions.DeparseSignature) (sqltype.Value,error) {
		value,err := sqltype.OIDValue(oidType,uint32(id))
		if err != nil { return sqltype.Value{},err }
		arg := plan.LiteralExpr{Info:expr.Info{Usage:oidType.Usage()},Value:value}
		node := functions.Deparse{Info:expr.Info{Usage:textType.Usage(),Property:expr.Properties{Volatility:expr.Stable}},Signature:signature,Args:[]expr.Expr{arg}}
		return node.Eval(ctx)
	}
	f := read.State().ProceduresNamed(public,"rd_f")[0]
	for i,want := range []string{"CREATE OR REPLACE FUNCTION public.rd_f(a integer DEFAULT 7)\n RETURNS integer\n LANGUAGE sql\nAS $function$SELECT $1$function$\n","a integer DEFAULT 7","a integer","integer"} {
		value,err := inquiry(f.ObjectID,functions.DeparseFunction+functions.DeparseSignature(i))
		if err != nil { t.Fatal(err) }
		if got,err := value.Text();err != nil || got != want { t.Fatal(got,err,want) }
	}
	for _, tc := range []struct{name, body string}{
		{"rd_atomic","BEGIN ATOMIC\n SELECT 7;\n RETURN 8;\nEND\n"},
		{"rd_alias","BEGIN ATOMIC\n SELECT 7 AS value;\nEND\n"},
		{"rd_relation","BEGIN ATOMIC\n SELECT rd_relation_source.id\n    FROM rd_relation_source;\nEND\n"},
	} {
		address := read.State().ProceduresNamed(public,tc.name)[0]
		value,err := inquiry(address.ObjectID,functions.DeparseFunction)
		if err != nil { t.Fatal(err) }
		want := "CREATE OR REPLACE FUNCTION public."+tc.name+"()\n RETURNS integer\n LANGUAGE sql\n"+tc.body
		if got,err := value.Text();err != nil || got != want { t.Fatal(got,err,want) }
	}
	// All actual baseline descriptors, not the release identity ledger. This
	// includes unavailable execution providers and every parsed standard body.
	for _,descriptor := range read.Descriptors() {
		d,ok := descriptor.(*catalog.Procedure)
		if !ok { continue }
		for _,signature := range []functions.DeparseSignature{functions.DeparseFunctionArguments,functions.DeparseFunctionIdentityArguments,functions.DeparseFunctionResult} {
			if _,err := inquiry(d.Address().ObjectID,signature);err != nil { t.Fatalf("%d %s: %v",d.Address().ObjectID,signature.Name(),err) }
		}
		if d.RoutineKind == catalog.AggregateRoutine { continue }
		value,err := inquiry(d.Address().ObjectID,functions.DeparseFunction)
		if err != nil { t.Fatalf("definition %d %s: %v",d.Address().ObjectID,d.Name(),err) }
		if value.IsNull() { t.Fatal("represented routine became NULL",d.Name()) }
	}
}
