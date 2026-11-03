package enginetests_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestXMLMapScopeProvider(t *testing.T){
	e:=engine.New();ctx:=context.Background();b:=enginetest.ExpressionBinder(t,ctx,e,sqltype.InputEnvironment{},nil)
	for _,tc:=range []struct{name string;oid uint32;op functions.XMLMappingOperation;schema,combined bool}{
		{"schema_to_xml",2933,functions.XMLSchemaRows,false,false},
		{"schema_to_xmlschema",2934,functions.XMLSchemaSchema,true,false},
		{"schema_to_xml_and_xmlschema",2935,functions.XMLSchemaCombined,true,true},
		{"database_to_xml",2936,functions.XMLDatabaseRows,false,false},
		{"database_to_xmlschema",2937,functions.XMLDatabaseSchema,true,false},
		{"database_to_xml_and_xmlschema",2938,functions.XMLDatabaseCombined,true,true},
	}{
		database:=strings.HasPrefix(tc.name,"database_");args:=[]string{"'absent'::name","false","false","''"};labels:=[]string{"schema","nulls","tableforest","targetns"};casts:=[]string{"name","boolean","boolean","text"};if database{args=args[1:];labels=labels[1:];casts=casts[1:]}
		for _,named:=range []bool{false,true}{
			written:=append([]string(nil),args...);if named{for i:=range written{written[i]=labels[i]+"=>"+written[i]};for i,j:=0,len(written)-1;i<j;i,j=i+1,j-1{written[i],written[j]=written[j],written[i]}}
			raw:=enginetest.Statement(t,"SELECT "+tc.name+"("+strings.Join(written,",")+")").(*parser.Select).Targets[0].(parser.FunctionExpr)
			node,err:=b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext});if err!=nil{t.Fatal(err)};recipe,ok:=node.(functions.XMLMapping);if !ok{t.Fatalf("provider %T",node)}
			if recipe.Operation!=tc.op||!tc.op.Scope()||tc.op.SchemaScope()==database||tc.op.DatabaseScope()!=database||tc.op.Schema()!=tc.schema||tc.op.Combined()!=tc.combined||tc.op.Relation()||tc.op.Cursor()||tc.op.Arity()!=len(args){t.Fatal("scope classifications",tc,recipe)}
			if recipe.CallRoutineReference()==nil||recipe.CallRoutineReference().RoutineOID()!=tc.oid||recipe.IntrinsicVolatility()!=expr.Stable||recipe.PlanningFold()!=expr.FoldNever||recipe.PreflightFold()!=expr.PreflightChildren{t.Fatal("routine/demand",recipe)}
			children:=recipe.Children();rebuilt,err:=recipe.Rebuild(expr.InfoOf(recipe),children);if err!=nil||!reflect.DeepEqual(recipe,rebuilt){t.Fatal("rebuild",err)}
			capture,ok,err:=recipe.Capture(expr.CaptureFunction);if err!=nil||!ok||capture.Check(recipe,len(args))!=nil{t.Fatal("capture",err)}
			render,err:=recipe.Render();if err!=nil||len(render.Args)!=len(args){t.Fatal("render",err)}
			for i:=range args{want:=i;if named{want=len(args)-1-i};if recipe.Order[i]!=want||named&&render.Args[i].Label!=labels[want]{t.Fatal("formal replay order",recipe.Order,render)}}
			types:=formatUnitTypes();fresh,changed,err:=stored.RetagTypes(b.BindingCatalog(),recipe,func(d *sqltype.Descriptor)*sqltype.Descriptor{return types[d.ID()]})
			if err!=nil||!changed{t.Fatal("scope compatible remap",changed,err)};remapped,ok:=fresh.(functions.XMLMapping)
			if !ok||remapped.Operation!=recipe.Operation||remapped.Order!=recipe.Order||remapped.Usage.Type!=types[sqltype.XML]||recipe.Usage.Type==remapped.Usage.Type||remapped.CallRoutineReference()==nil||remapped.CallRoutineReference().RoutineOID()!=tc.oid{t.Fatal("scope remap lost typed routine recipe")}
			for i,child:=range remapped.Children(){if child.ResultUsage().Type!=types[children[i].ResultUsage().Type.ID()]||child.ResultUsage().Type==children[i].ResultUsage().Type{t.Fatal("scope remap retained source operand descriptor")}}
		}
		for i:=range args{
			nulls:=append([]string(nil),args...);nulls[i]="NULL::"+casts[i]
			raw:=enginetest.Statement(t,"SELECT "+tc.name+"("+strings.Join(nulls,",")+")").(*parser.Select).Targets[0]
			node,err:=b.Bind(raw,analyze.ExpressionPolicy{Context:analyze.ProjectionContext});if err!=nil{t.Fatal(err)}
			value,err:=node.Eval(&exec.Context{EvalStatement:exec.EvalStatement{Context:ctx}});if err!=nil||!value.IsNull()||value.Type().ID()!=sqltype.XML{t.Fatal("strict scope demanded host",tc.name,i,value,err)}
			// Genuine parameter operands also suppress runtime host demand.
			parameters:=append([]string(nil),args...);parameters[i]="$1::"+casts[i]
			pb:=enginetest.ExpressionBinder(t,ctx,e,sqltype.InputEnvironment{},nil)
			rawParameter:=enginetest.Statement(t,"SELECT "+tc.name+"("+strings.Join(parameters,",")+")").(*parser.Select).Targets[0]
			parameterNode,err:=pb.Bind(rawParameter,analyze.ExpressionPolicy{Context:analyze.ProjectionContext});if err!=nil{t.Fatal(err)}
			signature,err:=pb.Finish();if err!=nil||signature.Count!=1{t.Fatal("parameter binding",signature,err)}
			value,err=parameterNode.Eval(&exec.Context{EvalStatement:exec.EvalStatement{Context:ctx,Parameters:[]sqltype.Value{sqltype.Null(signature.Types[0].Type)}}})
			if err!=nil||!value.IsNull()||value.Type().ID()!=sqltype.XML{t.Fatal("strict parameter demanded host",tc.name,i,value,err)}
		}
	}
	for _,op:=range []functions.XMLMappingOperation{functions.XMLTableRows,functions.XMLQueryRows,functions.XMLCursorRows,functions.XMLTableSchema,functions.XMLQuerySchema,functions.XMLCursorSchema,functions.XMLTableCombined,functions.XMLQueryCombined}{if op.Scope()||op.Arity()<4{t.Fatal("old mapper classification changed",op)}}
}
