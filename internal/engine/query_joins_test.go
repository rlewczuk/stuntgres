package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqlstate"
)

func queryJoinsFixture(t *testing.T, q string) (*Engine,*BoundQuery,*querySource) {
	t.Helper()
	e := New()
	for _,sql := range []string{"CREATE TABLE a(l TEXT,k INT,q INT)","CREATE TABLE b(r TEXT,q INT,k INT)","CREATE TABLE c(k INT,c TEXT)","CREATE TABLE wide(k BIGINT)","CREATE TABLE chars(k VARCHAR(3))","CREATE TABLE longer(k VARCHAR(5))","CREATE TABLE same(k VARCHAR(3))","CREATE TABLE unused(v INT)","INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)","INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)","INSERT INTO c VALUES(1,'c'),(1,'c')","INSERT INTO wide VALUES(1)","INSERT INTO chars VALUES('x')","INSERT INTO longer VALUES('x')","INSERT INTO same VALUES('x')"} { run(t,e,sql) }
	op,done,err := testOperation(e,context.Background())
	if err != nil { t.Fatal(err) }
	defer done()
	scope,err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	ast := statement(t,q)
	if _,err = scope.Describe(ast); err != nil { scope.Finish(err); t.Fatal(err) }
	e.mu.Lock()
	auth,err := e.authorizationLocked(op)
	var b binding
	if err == nil { b,err = e.describe(op.ctx,ast,auth,scope.lockedRef,scope.input,op,nil) }
	var source *querySource
	if err == nil { source,err = e.captureQuerySourcesLocked(scope,b.querySources,false) }
	e.mu.Unlock()
	if err == nil { err = selectQuerySources(op.ctx,source) }
	if finishErr := scope.Finish(err); finishErr != nil { t.Fatal(finishErr) }
	t.Cleanup(source.close)
	return e,b.query,source
}

func TestQueryJoinsMergedMetadataAndFrames(t *testing.T) {
	for _,q := range []string{"SELECT k,a.k,wide.k FROM a JOIN wide USING(k)","SELECT k,a.k,wide.k FROM wide JOIN a USING(k)"} {
		_,query,source := queryJoinsFixture(t,q)
		schema := query.Root.Schema()
		if schema[0].Usage.Type.ID()!=sqltype.Int8 || schema[1].Usage.Type.ID()!=sqltype.Int4 || schema[2].Usage.Type.ID()!=sqltype.Int8 || schema[0].OriginTable!=schema[2].OriginTable || schema[0].OriginAttribute!=schema[2].OriginAttribute || schema[0].OriginTable==schema[1].OriginTable { t.Fatal(q,schema) }
		result,n,err := finalizeQuery(context.Background(),query.Root,source,nil)
		if err!=nil || n!=2 { t.Fatal(result,n,err) }
		result.Close()
	}
	for _,tc := range []struct{q string; typmod int32; direct bool}{
		{"SELECT k FROM chars JOIN same USING(k)",7,true},
		{"SELECT k FROM chars JOIN longer USING(k)",-1,false},
	} {
		_,query,source := queryJoinsFixture(t,tc.q)
		column := query.Root.Schema()[0]
		if column.Usage.Typmod!=tc.typmod || (column.OriginTable!=0)!=tc.direct { t.Fatal(tc,column) }
		result,n,err := finalizeQuery(context.Background(),query.Root,source,nil)
		if err!=nil || n!=1 { t.Fatal(result,n,err) }
		result.Close()
	}
	e,query,source := queryJoinsFixture(t,"SELECT a.k,b.k,k FROM a JOIN b USING(k)")
	join := query.Root.(*projectNode).child.(*joinNode)
	if len(join.Schema())!=6 || len(query.Root.Schema())!=3 { t.Fatal(join.Schema(),query.Root.Schema()) }
	if source.sources[0].view.read()!=source.sources[1].view.read() || len(source.view.read().committed)!=2 { t.Fatal("not one selected multi-table view") }
	unused := catalogRef(t,e,source.view.Catalog(),"unused")
	if _,ok := source.view.read().committed[unused.Address]; ok { t.Fatal("unrelated root captured") }
	project := query.Root.(*projectNode)
	for i,want := range []int{1,5,1} { if project.expressions[i].(columnExpression).slot!=want { t.Fatal(project.expressions) } }
}

func TestQueryJoinsErrorsCancellationAndCleanup(t *testing.T) {
	for _,mode := range []string{"reject_cancel","admitted_error","rejected_error","late_target","merge_error","second_open","bad_width","bad_usage"} {
		t.Run(mode,func(t *testing.T){
			e,query,source := queryJoinsFixture(t,"SELECT a.k FROM a JOIN b ON a.k=b.k")
			children := append([]*querySource(nil),source.sources...)
			project := query.Root.(*projectNode)
			join := project.child.(*joinNode)
			ctx,cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("join provider sentinel")
			want := error(sentinel)
			calls:=0
			boolType,_ := e.CatalogRead().LookupType(sqltype.Bool)
			switch mode {
			case "reject_cancel":
				want=context.Canceled
				join.predicate=queryProbe(boolType.Usage(),func(c *EvalContext)(sqltype.Value,error){ calls++; if calls==3 {cancel()}; return sqltype.Boolean(boolType,false),nil })
			case "admitted_error","rejected_error":
				join.predicate=queryProbe(boolType.Usage(),func(c *EvalContext)(sqltype.Value,error){ calls++; if calls==3 {return sqltype.Value{},sentinel}; return sqltype.Boolean(boolType,mode=="admitted_error"),nil })
			case "late_target":
				original:=project.expressions[0]
				project.expressions[0]=queryProbe(original.ResultUsage(),func(c *EvalContext)(sqltype.Value,error){ calls++; if calls==3 {return sqltype.Value{},sentinel}; return original.Eval(c) })
			case "merge_error":
				usage:=project.expressions[0].ResultUsage()
				join.querySchema=append(join.querySchema,ColumnDescriptor{Usage:usage})
				join.merges=[]BoundExpr{queryProbe(usage,func(c *EvalContext)(sqltype.Value,error){calls++;if calls==3{return sqltype.Value{},sentinel};return c.Row[1],nil})}
			case "second_open":
				join.right.(*sourceNode).occurrence=len(children)
			case "bad_width": join.leftWidth++
			case "bad_usage":
				join.predicate=queryProbe(boolType.Usage(),func(c *EvalContext)(sqltype.Value,error){ return c.Row[1],nil })
			}
			rows,n,err:=finalizeQuery(ctx,query.Root,source,nil)
			if rows!=nil || n!=0 || err==nil { t.Fatal(rows,n,err) }
			if mode=="bad_width" || mode=="bad_usage" || mode=="second_open" {var diagnostic *sqlstate.Error;if errors.As(err,&diagnostic) {t.Fatal("invariant error acquired SQLSTATE",diagnostic)}} else if !errors.Is(err,want) {t.Fatal(err,want)}
			for _,child:=range children { if child.view!=nil || child.committed!=nil || child.own!=nil { t.Fatal("orphan child capture",child) } }
			if source.sources!=nil { t.Fatal("orphan source bundle") }
		})
	}
}

func TestQueryJoinsRejectedPairAllocation(t *testing.T) {
	_,query,source:=queryJoinsFixture(t,"SELECT a.k FROM a JOIN b ON FALSE")
	defer source.close()
	join:=query.Root.(*projectNode).child.(*joinNode)
	// Repeat immutable input occurrences without changing width or output count.
	right:=source.sources[1]
	original:=append([]rowVersion(nil),right.committed...)
	measure:=func(n int) uint64 {
		right.committed=make([]rowVersion,n)
		for i:=range right.committed { right.committed[i]=original[i%len(original)] }
		var before,after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for range 5 {
			stage,err:=openJoinNode(context.Background(),&queryState{source:source},join)
			if err!=nil || stage.count()!=0 { t.Fatal(stage,err) }
			stage.Close()
		}
		runtime.ReadMemStats(&after)
		return after.TotalAlloc-before.TotalAlloc
	}
	small,large:=measure(4),measure(4096)
	if large>small+8192 { t.Fatal("rejected-pair allocations scale with product",small,large) }
	_,single,singleSource:=queryCoreFixture(t,"SELECT id FROM t")
	rows,n,err:=finalizeQuery(context.Background(),single.Root,singleSource,nil)
	if err!=nil || n!=4 { t.Fatal(rows,n,err) }
	final:=rows.(*finalRows)
	if final.source!=singleSource || final.values!=nil || final.source.flat!=nil || final.source.anonymous!=nil { t.Fatal("single-source direct scan copied tuples",final) }
	rows.Close()
}

func TestQueryJoinsZeroWidthAndHeldRoots(t *testing.T) {
	e:=New()
	for _,q:=range []string{"CREATE TABLE a(v INT)","CREATE TABLE b(v INT)","INSERT INTO a VALUES(1),(2)","INSERT INTO b VALUES(1),(2),(3)","ALTER TABLE a DROP COLUMN v","ALTER TABLE b DROP COLUMN v"} {run(t,e,q)}
	result:=run(t,e,"SELECT * FROM a CROSS JOIN b")
	if result.Count!=6 || len(result.Rows.Schema())!=0 {t.Fatal(result)}
	for range 6 { if present,err:=result.Rows.Next(context.Background(),nil); !present || err!=nil {t.Fatal(present,err)} }
	if present,err:=result.Rows.Next(context.Background(),nil); present || err!=nil {t.Fatal(present,err)}
	result.Rows.Close()
	for _,replacement:=range []string{"append","reset","restore"} {
		for _,projection:=range []string{"a.k,b.k","a.k+1,b.k+1"} {
			e,query,source:=queryJoinsFixture(t,"SELECT "+projection+" FROM a JOIN b USING(k)")
			rows,n,err:=finalizeQuery(context.Background(),query.Root,source,nil)
			if err!=nil || n!=4 {t.Fatal(rows,n,err)}
			schema:=append([]ColumnDescriptor(nil),rows.Schema()...)
			switch replacement {
			case "append": run(t,e,"INSERT INTO b VALUES('new',9,1)")
			case "reset": err=e.Reset(context.Background())
			case "restore": err=e.Restore(context.Background(),strings.NewReader("CREATE TABLE a(k INT); CREATE TABLE b(k INT); INSERT INTO a VALUES(8); INSERT INTO b VALUES(8)"))
			}
			if err!=nil || !reflect.DeepEqual(rows.Schema(),schema) {t.Fatal(err,rows.Schema(),schema)}
			scratch:=make([]sqltype.Value,2)
			want:=int64(1)
			if projection=="a.k+1,b.k+1" {want=2}
			for range 4 {
				present,err:=rows.Next(context.Background(),scratch)
				l,_:=scratch[0].Integer(); r,_:=scratch[1].Integer()
				if !present || err!=nil || l!=want || r!=want {t.Fatal(present,err,l,r,want)}
			}
			rows.Close()
			if replacement=="append" {fresh:=run(t,e,"SELECT a.k FROM a JOIN b USING(k)"); if fresh.Count!=6 {t.Fatal(fresh)};fresh.Rows.Close()}
			if replacement=="restore" {fresh:=run(t,e,"SELECT a.k FROM a JOIN b USING(k)"); if fresh.Count!=1 {t.Fatal(fresh)};fresh.Rows.Close()}
		}
	}
}

func TestQueryJoinsAdmittedFrameLayout(t *testing.T) {
	_,query,source:=queryJoinsFixture(t,"SELECT a.k FROM a JOIN b ON TRUE")
	join:=query.Root.(*projectNode).child.(*joinNode)
	left:=append([]sqltype.Value(nil),source.sources[0].row(0)...)
	right:=append([]sqltype.Value(nil),source.sources[1].row(0)...)
	stage,err:=openJoinNode(context.Background(),&queryState{source:source},join)
	if err!=nil {t.Fatal(err)}
	defer stage.Close()
	if stage.count()!=16 || stage.source.flatWidth!=6 || len(stage.source.flat)!=96 || len(stage.source.flat)>cap(stage.source.flat) || cap(stage.source.flat)>2*len(stage.source.flat) {t.Fatal(stage.count(),stage.source)}
	if !reflect.DeepEqual(left,source.sources[0].row(0)) || !reflect.DeepEqual(right,source.sources[1].row(0)) {t.Fatal("joined scratch mutated a retained child row")}
}

func TestQueryJoinsEmptyOperandStillPreflightsOtherChild(t *testing.T) {
	e,query,source:=queryJoinsFixture(t,"SELECT a.k FROM a CROSS JOIN (b JOIN c ON b.k=int4($1::text))")
	children:=append([]*querySource(nil),source.sources...)
	source.committed=nil
	text,_:=e.CatalogRead().LookupType(sqltype.Text)
	rows,n,err:=finalizeQuery(context.Background(),query.Root,source,[]sqltype.Value{sqltype.String(text,"oops")})
	state(t,err,"22P02")
	if rows!=nil || n!=0 {t.Fatal("preflight failure exposed partial output",rows,n)}
	for _,child:=range children {if child.view!=nil || child.committed!=nil || child.own!=nil {t.Fatal("preflight orphan",child)}}
}

func TestQueryJoinsVisibilityReleaseAuthority(t *testing.T) {
	_,query,source:=queryJoinsFixture(t,"SELECT a.k FROM a JOIN b USING(k)")
	for _,child:=range source.sources {
		data:=child.view.read()
		forged:=VisibilityView{self:child.view}
		forged.data.Store(data)
		forged.Release()
		if forged.Retain()!=nil || child.view.read()!=data {t.Fatal("forged view acquired release authority")}
		retained:=child.view.Retain()
		retained.Release()
		retained.Release()
		if child.view.read()!=data {t.Fatal("independent release invalidated source capture")}
	}
	rows,n,err:=finalizeQuery(context.Background(),query.Root,source,nil)
	if err!=nil || n!=4 {t.Fatal(rows,n,err)}
	rows.Close()
}
