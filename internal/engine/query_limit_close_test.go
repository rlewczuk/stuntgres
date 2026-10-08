package engine

import (
	"context"
	"errors"
	"testing"
	"stuntgres/internal/sqltype"
)

func TestQueryLimitRequiredClose(t *testing.T) {
	for _,bounded:=range []bool{false,true} {
		for _,primary:=range []bool{false,true} {
			t.Run(map[bool]string{false:"all",true:"one"}[bounded]+map[bool]string{false:"/close veto",true:"/primary preserved"}[primary],func(t *testing.T){
				e,query,input:=queryCoreFixture(t,"SELECT id FROM t")
				project:=query.Root.(*projectNode)
				cleanup:=errors.New("required inner close")
				failure:=errors.New("primary projection failure")
				closes:=0
				inner:=indexedStage{source:input,schema:project.Schema(),expressions:project.expressions,evaluation:EvalContext{Scope:1,Frame:1},finish:func()error{closes++;return cleanup}}
				if primary {inner.expressions=[]BoundExpr{queryProbe(project.Schema()[0].Usage,func(*EvalContext)(sqltype.Value,error){return sqltype.Value{},failure})}}
				bridge:=&querySource{ref:input.ref,nestedRoot:project,nestedInputs:input,nestedStage:&inner,materialized:true,flatWidth:1}
				var child LogicalNode=&sourceNode{querySchema:project.Schema(),ref:input.ref}
				if bounded {child=&limitNode{querySchema:project.Schema(),child:child,count:queryLimitBound(t,e,1)}}
				outer:=&projectNode{querySchema:project.Schema(),child:child,expressions:project.expressions}
				rows,n,err:=finalizeQuery(context.Background(),outer,bridge,nil)
				want:=cleanup
				if primary {want=failure}
				if rows!=nil||n!=0||err!=want||closes!=1||input.view!=nil||bridge.nestedStage!=nil {t.Fatal(rows,n,err,want,closes,input.view,bridge.nestedStage)}
			})
		}
	}
}

func TestQueryLimitEOFCloses(t *testing.T) {
	_,query,input:=queryCoreFixture(t,"SELECT id FROM t")
	project:=query.Root.(*projectNode)
	failure:=errors.New("EOF close failure")
	closes:=0
	child:=indexedStage{source:input,schema:project.child.Schema(),selection:[]int{0},finish:func()error{closes++;return failure}}
	stage:=producingStage(&child,project.Schema())
	stage.expressions=project.expressions
	stage.evaluation = EvalContext{Scope:1,Frame:1}
	stage.produce=func(ctx context.Context,out *indexedStage)(bool,error){i,present,err:=child.nextOccurrence(ctx);if err!=nil||!present{return present,err};retainOccurrence(out,&child,i);return true,nil}
	values:=make([]sqltype.Value,1)
	if present,err:=stage.Next(context.Background(),values);!present||err!=nil{t.Fatal(present,err)}
	if present,err:=stage.Next(context.Background(),values);present||err!=failure{t.Fatal(present,err)}
	if err:=stage.Close();err!=nil||closes!=1||stage.source!=nil||stage.produce!=nil||stage.finish!=nil{t.Fatal(err,closes,stage)}
}
