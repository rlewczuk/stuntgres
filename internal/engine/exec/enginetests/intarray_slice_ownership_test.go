package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIntarraySliceValues(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"CREATE EXTENSION intarray")
	for _,tc := range []struct { name string; arity int; want string }{
		{"icount",1,"3"},{"idx",2,"1"},{"subarray",2,"{22,11,22}"},{"subarray",3,"{22,11}"},{"intset",1,"{22}"},
	} {
		b := enginetest.ExpressionBinder(t,t.Context(),e,sqltype.InputEnvironment{},nil)
		args := []parser.Expr{intarrayOwnershipCast("{22,11,22}")}
		if tc.name == "intset" { args[0]=enginetest.PreparedBoundaryInteger(22) }
		for len(args)<tc.arity { args=append(args,enginetest.PreparedBoundaryInteger(1)) }
		if tc.name=="idx" { args[1]=enginetest.PreparedBoundaryInteger(22) }
		if tc.arity==3 { args[2]=enginetest.PreparedBoundaryInteger(2) }
		bound,err := b.Bind(nullCountRaw(tc.name,args...),analyze.ExpressionPolicy{Context:analyze.ProjectionContext})
		if err!=nil { t.Fatal(err) }
		node,ok := bound.(functions.IntarraySlice); if !ok { t.Fatalf("selected node %T",bound) }
		integer:=node.Formals[0]
		if integer.IsArray() { integer=integer.Element() }
		twentyTwo,_:=sqltype.Integer(integer,22)
		eleven,_:=sqltype.Integer(integer,11)
		dimensions:=[]sqltype.ArrayDimension{{Lower:0,Length:3}}
		elements:=[]sqltype.Value{twentyTwo,eleven,twentyTwo}
		var source sqltype.Value
		for i:=range node.Operation.Arity() {
			value:=twentyTwo
			if node.Formals[i].IsArray() {
				value,err=sqltype.ArrayValue(node.Formals[i],dimensions,elements)
				source=value
			} else if i>0 && tc.name!="idx" {
				n:=int64(1); if i==2 { n=2 }; value,err=sqltype.Integer(integer,n)
			}
			if err!=nil { t.Fatal(err) }
			node.Args[i]=plan.LiteralExpr{Info:expr.Info{Usage:value.Type().Usage()},Value:value}
		}
		group:=exec.FoldCaptureCandidate{}
		ctx:=&exec.Context{EvalStatement:exec.EvalStatement{Context:t.Context(),Catalog:e.CatalogRead(),FoldCaptures:&group}}
		value,err:=exec.FoldPlanningExpression(ctx,node)
		if err!=nil { t.Fatal(err) }
		text,err:=value.Type().Output(t.Context(),value)
		if err!=nil || text!=tc.want { t.Fatal(tc,text,err) }
		index:=-1
		for i,entry:=range group.Entries { if entry.Callable==node.RoutineReference { index=i } }
		if index<0 || group.Entries[index].Value!=value { t.Fatal("selected typed capture absent") }
		retained,backing:=group.Entries[index],&group.Entries[0]
		dimensions[0].Lower=99; elements[0]=sqltype.Null(integer)
		if source.Type()!=nil && (source.ArrayDimension(0).Lower!=0 || source.ArrayElement(0).IsNull()) { t.Fatal("constructor scratch escaped into source") }
		if value.Type().IsArray() && (value.ArrayDimension(0).Lower!=1 || value.ArrayElement(0).IsNull()) { t.Fatal("constructor scratch escaped into retained result") }
		group.Private=false
		again,err:=exec.FoldPlanningExpression(ctx,node)
		if err!=nil || again!=value || group.Private || &group.Entries[0]!=backing || !enginetest.SamePublishedFold(retained,group.Entries[index]) { t.Fatal("accepted backing changed",err) }
		canceled,cancel:=context.WithCancel(t.Context()); cancel()
		failed:=exec.FoldCaptureCandidate{}
		ctx.Context,ctx.FoldCaptures=canceled,&failed
		if _,err:=exec.FoldPlanningExpression(ctx,node); !errors.Is(err,context.Canceled) || failed.Sealed { t.Fatal("canceled candidate published",err) }
		if !enginetest.SamePublishedFold(retained,group.Entries[index]) || &group.Entries[0]!=backing { t.Fatal("failed candidate mutated accepted value") }
	}
}
