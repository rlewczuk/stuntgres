package enginetests_test

import (
	"context"
	"reflect"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestXMLMapRowsProvider(t *testing.T){
	e:=engine.New();ctx:=context.Background();b:=enginetest.ExpressionBinder(t,ctx,e,sqltype.InputEnvironment{},nil)
	for _,tc:=range []struct{q string;oid uint32;stable bool}{
		{`SELECT table_to_xml(999999::oid::regclass,false,false,'')`,2923,true},
		{`SELECT query_to_xml('SELECT 1',false,false,'')`,2924,false},
		{`SELECT cursor_to_xml('c',1,false,false,'')`,2925,false},
		{`SELECT table_to_xmlschema(999999::oid::regclass,false,false,'')`,2926,true},
		{`SELECT query_to_xmlschema('SELECT 1',false,false,'')`,2927,false},
		{`SELECT cursor_to_xmlschema('c',false,false,'')`,2928,false},
		{`SELECT table_to_xml_and_xmlschema(999999::oid::regclass,false,false,'')`,2929,true},
		{`SELECT query_to_xml_and_xmlschema('SELECT 1',false,false,'')`,2930,false},
		{`SELECT query_to_xml(targetns=>'urn:x',query=>'SELECT 1',tableforest=>false,nulls=>false)`,2924,false},
	}{raw:=enginetest.Statement(t,tc.q).(*parser.Select).Targets[0].(parser.FunctionExpr);node,err:=b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext});if err!=nil{t.Fatal(tc.q,err)};recipe,ok:=node.(functions.XMLMapping);if !ok{t.Fatalf("provider %T",node)}
		if recipe.CallRoutineReference()==nil||recipe.CallRoutineReference().RoutineOID()!=tc.oid||recipe.PlanningFold()!=expr.FoldNever||recipe.PreflightFold()!=expr.PreflightChildren{t.Fatal("routine identity/demand",tc.q,recipe)}
		volatility:=expr.Volatile;if tc.stable{volatility=expr.Stable};if recipe.IntrinsicVolatility()!=volatility{t.Fatal("volatility",tc.q)}
		children:=recipe.Children();saved:=append([]expr.Expr(nil),children...)
		rebuilt,err:=recipe.Rebuild(expr.InfoOf(recipe),children);if err!=nil||!reflect.DeepEqual(recipe,rebuilt)||!recipe.SameGroup(rebuilt,func(a,b expr.Expr)bool{return reflect.DeepEqual(a,b)},true){t.Fatal("rebuild changed selected recipe",err)}
		capture,ok,err:=recipe.Capture(expr.CaptureFunction);if err!=nil||!ok||capture.Check(recipe,len(raw.Args))!=nil{t.Fatal("capture",capture,err)}
		rendering,err:=recipe.Render();if err!=nil||len(rendering.Args)!=len(raw.Args){t.Fatal("render",err)}
		if len(raw.ArgNames)!=0{if recipe.Order!=([5]int{3,0,2,1,0}){t.Fatal("named evaluation order",recipe.Order)};for i,want:=range []string{"targetns","query","tableforest","nulls"}{if rendering.Args[i].Label!=want{t.Fatal("render changed written demand order",rendering)}}}
		children[0]=nil;if !reflect.DeepEqual(saved,recipe.Children()){t.Fatal("children exposed mutable provider backing")}
		if _,err:=recipe.Rebuild(expr.InfoOf(recipe),nil);err==nil{t.Fatal("missing operand admitted")}
		forged:=recipe;forged.Order[0]=forged.Order[1];if err:=forged.Validate();err==nil{t.Fatal("duplicate formal slot admitted")}
	}
	// Actual strict operand bindings evaluate with no Host. No fake runtime can
	// conceal an accidental demand for a query, relation, session or cursor.
	for _,q:=range []string{`SELECT query_to_xml('SELECT nope',NULL::boolean,false,'')`,`SELECT table_to_xml(999999::oid::regclass,false,NULL::boolean,'')`,`SELECT cursor_to_xml('missing',NULL::int,false,false,'')`}{raw:=enginetest.Statement(t,q).(*parser.Select).Targets[0];node,err:=b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext});if err!=nil{t.Fatal(err)};value,err:=node.Eval(&exec.Context{EvalStatement:exec.EvalStatement{Context:ctx}});if err!=nil||!value.IsNull()||value.Type().ID()!=sqltype.XML{t.Fatal("strict binding demanded authority",q,value,err)}}
}
