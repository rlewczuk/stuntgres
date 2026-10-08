package engine

import (
	"context"
	"testing"
	"stuntgres/internal/sqltype"
)

func TestQueryLimitPipelineCancellation(t *testing.T) {
	t.Run("nonproductive join",func(t *testing.T){
		e,query,source:=queryCoreFixture(t,"SELECT id FROM t")
		project:=query.Root.(*projectNode)
		bt,_:=e.CatalogRead().LookupType(sqltype.Bool)
		ctx,cancel:=context.WithCancel(context.Background());defer cancel()
		calls:=0
		join:=&joinNode{querySchema:project.child.Schema(),left:project.child,right:&sourceNode{occurrence:1},leftWidth:len(project.child.Schema()),predicate:queryProbe(bt.Usage(),func(*EvalContext)(sqltype.Value,error){calls++;cancel();return sqltype.Boolean(bt,false),nil})}
		project.child=&limitNode{querySchema:join.Schema(),child:join,count:queryLimitBound(t,e,1)}
		inputs:=&querySource{sources:[]*querySource{source,{anonymous:[][]sqltype.Value{{}}}}}
		rows,n,err:=finalizeQuery(ctx,project,inputs,nil)
		if rows!=nil||n!=0||err!=context.Canceled||calls!=1||source.view!=nil{t.Fatal(rows,n,err,calls,source.view)}
	})
	for _,mode:=range []string{"NULL","tie"}{
		t.Run("sort comparator "+mode,func(t *testing.T){
			e,query,source:=queryCoreFixture(t,"SELECT id FROM t ORDER BY v")
			project:=query.Root.(*projectNode)
			sort:=project.child.(*sortNode)
			typ,_:=e.CatalogRead().LookupType(sqltype.Int4)
			value:=sqltype.Null(typ)
			if mode=="tie"{value,_=sqltype.Integer(typ,1)}
			ctx:=&queryCountingContext{Context:context.Background(),remaining:1000}
			calls:=0
			sort.cacheWidth=1
			sort.keys[0].cache=0
			sort.keys[0].expression=queryProbe(typ.Usage(),func(*EvalContext)(sqltype.Value,error){calls++;if calls==3{ctx.remaining=9};return value,nil})
			project.child=&limitNode{querySchema:sort.Schema(),child:sort,count:queryLimitBound(t,e,1)}
			// Three keys complete. Nine subsequent checks enter the first
			// comparison before either NULL/tie shortcut can publish a row.
			rows,n,err:=finalizeQuery(ctx,project,source,nil)
			if rows!=nil||n!=0||err!=context.Canceled||calls!=3||source.view!=nil{t.Fatal(rows,n,err,calls,source.view)}
		})
	}
}
