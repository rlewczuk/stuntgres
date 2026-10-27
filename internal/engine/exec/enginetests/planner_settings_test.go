package enginetests_test

import (
	"context"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
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
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Observe genuine provider execution without replacing the provider or adding
// production hooks. Err remains the ordinary underlying context operation.
type plannerExecutionContext struct { context.Context; hash, index, gather, partial atomic.Bool }
func (c *plannerExecutionContext) Err() error {
	var pcs [40]uintptr
	n := runtime.Callers(1,pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		f,more := frames.Next()
		if strings.Contains(f.Function,"sqltype.Grouping.Hash") { c.hash.Store(true) }
		if strings.Contains(f.Function,"IndexHash).Hash") { c.index.Store(true) }
		if strings.Contains(f.Function,"exec.GatherQueryRuns") { c.gather.Store(true) }
		if strings.Contains(f.Function,"exec.FinalizeParallelAggregate") { c.partial.Store(true) }
		if !more { break }
	}
	return c.Context.Err()
}
func plannerTypes(t *testing.T) (integer, bigint, boolean *sqltype.Descriptor) {
	t.Helper()
	for _, d := range sqltype.Builtins() { switch d.ID() { case sqltype.Int4: integer=d; case sqltype.Int8: bigint=d; case sqltype.Bool: boolean=d } }
	if integer==nil || bigint==nil || boolean==nil { t.Fatal("missing builtin types") }; return
}
func plannerValues(t *testing.T, integer *sqltype.Descriptor) [][]sqltype.Value {
	t.Helper(); var rows [][]sqltype.Value
	for _, n := range []int64{1,1,2} { v,err:=sqltype.Integer(integer,n); if err!=nil { t.Fatal(err) }; rows=append(rows,[]sqltype.Value{v}) }
	return append(rows,[]sqltype.Value{sqltype.Null(integer)})
}
func plannerColumn(integer *sqltype.Descriptor, slot int) plan.ColumnExpr { return plan.ColumnExpr{Info:expr.Info{Usage:integer.Usage(),Property:expr.Properties{RowDependent:true}},Scope:1,Frame:1,Slot:slot} }

func TestPlannerSettingsAggregateSelection(t *testing.T) {
	integer,bigint,_:=plannerTypes(t)
	grouping,err:=sqltype.BindGrouping(integer.Usage()); if err!=nil { t.Fatal(err) }
	schema:=plan.QuerySchema{{Name:"k",Usage:integer.Usage()}}
	column:=plannerColumn(integer,0)
	for _, disabled:=range []bool{false,true} {
		for _, distinct:=range []bool{false,true} {
			source:=&exec.Source{Anonymous:plannerValues(t,integer)}
			state:=&exec.State{Source:source,Methods:plan.PlannerMethods{NoHashAgg:disabled}}
			leaf:=&plan.SourceNode{QuerySchema:schema}
			var root plan.Node
			want:=3
			if distinct { root=&plan.DistinctNode{QuerySchema:schema,Child:leaf,Expressions:[]expr.Expr{column},Grouping:[]plan.Grouping{{Grouping:grouping}}} } else {
				out:=append(append(plan.QuerySchema(nil),schema...),catalog.Column{Name:"count",Usage:bigint.Usage()})
				root=&plan.AggregateNode{QuerySchema:out,Child:leaf,Keys:[]expr.Expr{column},Grouping:[]plan.Grouping{{Grouping:grouping}},Sets:[][]int{{0},{0}},Calls:[]plan.AggregateCall{{Provider:functions.NewCountProvider(bigint.Usage())}}}
				want=6
			}
			ctx:=&plannerExecutionContext{Context:context.Background()}
			stage,err:=exec.OpenNode(ctx,state,root)
			if err!=nil { t.Fatal(disabled,distinct,err) }
			if err:=stage.Drain(ctx); err!=nil { t.Fatal(err) }
			if stage.Count()!=want || ctx.hash.Load()==disabled { t.Fatalf("disabled=%t distinct=%t groups=%d hash=%t",disabled,distinct,stage.Count(),ctx.hash.Load()) }
			if !distinct {
				counts:=map[int64]int{}
				for i:=range stage.Count() { row:=stage.Source.Row(stage.Index(i)); n,err:=row[1].Integer(); if err!=nil { t.Fatal(err) }; counts[n]++ }
				if !reflect.DeepEqual(counts,map[int64]int{1:4,2:2}) { t.Fatal("duplicate sets shared groups",counts) }
			}
			stage.Close()
		}
	}
	t.Run("child_snapshot",func(t *testing.T) {
		e:=engine.New()
		op,done,err:=enginetest.Operation(e,context.Background()); if err!=nil { t.Fatal(err) }; defer done()
		scope,err:=e.StartStatement(op); if err!=nil { t.Fatal(err) }; defer scope.Finish(nil)
		scope.PlannerMethods=plan.PlannerMethods{NoHashAgg:true}
		parent:=&exec.State{Source:&exec.Source{Anonymous:plannerValues(t,integer),Host:core.HostOf(scope)}}
		parent.CapturePlannerAdmission()
		scope.PlannerMethods=plan.PlannerMethods{}
		leaf:=&plan.SourceNode{QuerySchema:schema}
		distinct:=&plan.DistinctNode{QuerySchema:schema,Child:leaf,Expressions:[]expr.Expr{column},Grouping:[]plan.Grouping{{Grouping:grouping}}}
		project:=&plan.ProjectNode{QuerySchema:schema,Child:distinct,Frame:1,Expressions:[]expr.Expr{column}}
		derived:=&plan.DerivedNode{QuerySchema:schema,Query:&plan.Query{Root:project}}
		ctx:=&plannerExecutionContext{Context:context.Background()}
		stage,err:=exec.OpenDerivedNode(ctx,parent,derived); if err!=nil { t.Fatal(err) }
		defer stage.Close()
		if err:=stage.Drain(ctx); err!=nil || stage.Count()!=3 || ctx.hash.Load() { t.Fatal("child reread changed host",stage.Count(),ctx.hash.Load(),err) }
		if !parent.Methods.NoHashAgg { t.Fatal("parent snapshot changed") }
	})
}

func TestPlannerSettingsJoinSelection(t *testing.T) {
	integer,_,boolean:=plannerTypes(t)
	schema:=plan.QuerySchema{{Usage:integer.Usage()}}
	left,right:=plannerColumn(integer,0),plannerColumn(integer,1)
	predicate,err:=sqltype.BindComparisonPredicate(sqltype.CompareEqual,integer,integer,boolean,sqltype.CollationRef{},false); if err!=nil { t.Fatal(err) }
	join:=&plan.JoinNode{QuerySchema:append(append(plan.QuerySchema(nil),schema...),schema...),Left:&plan.SourceNode{QuerySchema:schema,Occurrence:0},Right:&plan.SourceNode{QuerySchema:schema,Occurrence:1},LeftWidth:1,RightWidth:1,Kind:parser.JoinInner,Frame:1,Predicate:plan.ComparisonExpr{Info:expr.Info{Usage:boolean.Usage(),Property:expr.Properties{RowDependent:true}},Left:left,Right:right,Operator:"=",Predicate:predicate}}
	if !exec.HashJoinEligible(join,1) || exec.HashJoinEligible(join,2) { t.Fatal("shape/frame disagreement") }
	for _, disabled:=range []bool{false,true} {
		rows:=plannerValues(t,integer)
		source:=&exec.Source{Sources:[]*exec.Source{{Anonymous:rows,Occurrence:0},{Anonymous:rows,Occurrence:1}}}
		ctx:=&plannerExecutionContext{Context:context.Background()}
		stage,err:=exec.OpenNode(ctx,&exec.State{Source:source,Methods:plan.PlannerMethods{NoHashJoin:disabled}},join)
		if err!=nil { t.Fatal(err) }; if err:=stage.Drain(ctx); err!=nil { t.Fatal(err) }
		if stage.Count()!=5 || ctx.hash.Load()==disabled { t.Fatalf("disabled=%t count=%d hash=%t",disabled,stage.Count(),ctx.hash.Load()) }; stage.Close()
	}
	copy:=*join; copy.CorrelatedRight=true
	if exec.HashJoinEligible(&copy,1) { t.Fatal("correlated right admitted") }
}

func TestPlannerSettingsIndexSelection(t *testing.T) {
	integer,_,boolean:=plannerTypes(t)
	usage:=integer.Usage()
	class,ok:=sqltype.LookupHashIndexClass("int4_ops"); if !ok { t.Fatal("missing hash class") }
	e:=engine.New()
	// Capture genuine catalog support members, not just the class identity.
	class,err:=routine.BindClassSupport(e.CatalogRead(),class); if err!=nil { t.Fatal(err) }
	provider,err:=sqltype.BindIndexHash(usage,class); if err!=nil { t.Fatal(err) }
	predicate,err:=sqltype.BindComparisonPredicate(sqltype.CompareEqual,integer,integer,boolean,sqltype.CollationRef{},false); if err!=nil { t.Fatal(err) }
	view:=catalog.NewRead(&e.Catalog.Owner,catalog.NewState(1))
	ref:=e.Catalog.Domain.Ref(1,ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:20000})
	column:=plannerColumn(integer,0); column.Origin=ref; column.Origin.Address.SubID=1
	value,err:=sqltype.Integer(integer,1); if err!=nil { t.Fatal(err) }
	hash,err:=provider.Hash(context.Background(),value); if err!=nil { t.Fatal(err) }
	root:=&storage.HashRoot{Binding:storage.HashBinding{Slot:0,Usage:usage,Class:class,Provider:provider},Buckets:map[uint64][]storage.HashEntry{hash:{{Key:value,Row:1,Version:1,Position:0},{Key:value,Row:2,Version:2,Position:1}}}}
	for _, disabled:=range []bool{false,true} {
		source:=&exec.Source{Ref:ref,PlannerCosts:plan.DefaultPlannerCosts(),PlannerRows:1000,Committed:[]storage.RowVersion{{Row:1,Version:1,Values:[]sqltype.Value{value}},{Row:2,Version:2,Values:[]sqltype.Value{value}}},HashIndexes:[]exec.CapturedHashIndex{{Name:"pm_idx",Definition:&catalog.IndexDefinition{Keys:[]catalog.IndexKey{{Attribute:1}}},Root:root,Usage:usage}}}
		source.View=visibility.NewView(&visibility.Data{Catalog:view})
		defer source.Close()
		leaf:=&plan.SourceNode{QuerySchema:plan.QuerySchema{{Usage:usage}},Ref:ref}
		filter:=&plan.FilterNode{QuerySchema:leaf.Schema(),Child:leaf,Predicate:plan.ComparisonExpr{Info:expr.Info{Usage:boolean.Usage()},Left:column,Right:plan.LiteralExpr{Info:expr.Info{Usage:usage},Value:value},Operator:"=",Predicate:predicate}}
		exec.AdmitScan(&plan.ProjectNode{QuerySchema:leaf.Schema(),Child:filter,Frame:1},source)
		state:=&exec.State{Source:source,HashFilter:filter,Methods:plan.PlannerMethods{NoIndexScan:disabled}}
		ctx:=&plannerExecutionContext{Context:context.Background()}
		stage,err:=exec.OpenNode(ctx,state,filter); if err!=nil { t.Fatal(err) }
		if (stage.HashCandidates==nil)!=disabled || ctx.index.Load()==disabled { t.Fatalf("disabled=%t candidates=%+v index hash=%t",disabled,stage.HashCandidates,ctx.index.Load()) }
		if err:=stage.Drain(ctx); err!=nil || stage.Count()!=2 { t.Fatal(stage.Count(),err) }; stage.Close()
	}
}

func TestPlannerSettingsGatherSelection(t *testing.T) {
	integer,bigint,_:=plannerTypes(t)
	comparison,err:=sqltype.BindComparison(integer,integer,sqltype.CollationRef{},false); if err!=nil { t.Fatal(err) }
	grouping,err:=sqltype.BindGrouping(integer.Usage()); if err!=nil { t.Fatal(err) }
	e:=engine.New()
	op,done,err:=enginetest.Operation(e,context.Background()); if err!=nil { t.Fatal(err) }; defer done()
	scope,err:=e.StartStatement(op); if err!=nil { t.Fatal(err) }; defer scope.Finish(nil)
	ns:=ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:2200}
	address:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:20000}
	state:=catalog.NewState(1)
	state.SetDescriptor(ns,&catalog.Namespace{ObjectAddress:ns,ObjectName:"public"})
	state.SetDescriptor(address,&catalog.Relation{ObjectAddress:address,ObjectNamespace:ns,ObjectName:"pm_gather",RelationKind:catalog.TableRelation})
	view:=catalog.NewRead(&e.Catalog.Owner,state)
	ref:=e.Catalog.Domain.Ref(1,address)
	schema:=plan.QuerySchema{{Name:"k",Usage:integer.Usage()}}
	column:=plannerColumn(integer,0)
	leaf:=&plan.SourceNode{QuerySchema:schema,Ref:ref}
	// Uncached keys borrow the source slot; cache zero requires owned OrderValues.
	ordered:=&plan.SortNode{QuerySchema:schema,Child:leaf,Keys:[]plan.SortKey{{Expression:column,Slot:0,Cache:-1,Comparison:comparison}}}
	for _, disabled:=range []bool{false,true} {
		for _, sorted:=range []bool{false,true} {
			scope.PlannerMethods=plan.PlannerMethods{NoGatherMerge:disabled}
			rows:=plannerValues(t,integer)
			// Force both worker-local sorting and cross-run merging to do work.
			rows[0],rows[2]=rows[2],rows[0]
			committed:=make([]storage.RowVersion,len(rows))
			for i,row:=range rows { committed[i]=storage.RowVersion{Row:ids.RowID(i+1),Version:ids.VersionID(i+1),Values:row} }
			source:=&exec.Source{Ref:ref,View:visibility.NewView(&visibility.Data{Catalog:view}),Committed:committed,Host:core.HostOf(scope),PlannerCosts:plan.PlannerCosts{Tuple:1},PlannerRows:float64(len(rows)),PlannerColumns:1}
			var child plan.Node=leaf; if sorted { child=ordered }
			project:=&plan.ProjectNode{QuerySchema:schema,Child:child,Frame:1,Expressions:[]expr.Expr{column}}
			if _,_,ok:=exec.ParallelQueryPlan(project,source); !ok { t.Fatal("fixture not admitted") }
			ctx:=&plannerExecutionContext{Context:context.Background()}
			result,n,err:=exec.FinalizeSelect(ctx,project,source,nil,2)
			if err!=nil || n!=4 { t.Fatal(n,err) }
			wantGather:=!sorted || !disabled
			if ctx.gather.Load()!=wantGather { t.Fatalf("disabled=%t sorted=%t gathered=%t",disabled,sorted,ctx.gather.Load()) }
			if sorted {
				row:=make([]sqltype.Value,1)
				for i,want:=range []int64{1,1,2} {
					ok,err:=result.Next(ctx,row); if err!=nil || !ok { t.Fatal("missing ordered row",i,err) }
					got,err:=row[0].Integer(); if err!=nil || got!=want { t.Fatal("ordered value",i,got,err) }
				}
				ok,err:=result.Next(ctx,row); if err!=nil || !ok || !row[0].IsNull() { t.Fatal("missing trailing NULL",err) }
				ok,err=result.Next(ctx,row); if err!=nil || ok { t.Fatal("unexpected extra row",err) }
			}
			result.Close()
		}
		scope.PlannerMethods=plan.PlannerMethods{NoHashAgg:disabled}
		rows:=plannerValues(t,integer)
		committed:=make([]storage.RowVersion,len(rows))
		for i,row:=range rows { committed[i]=storage.RowVersion{Row:ids.RowID(i+1),Version:ids.VersionID(i+1),Values:row} }
		source:=&exec.Source{Ref:ref,View:visibility.NewView(&visibility.Data{Catalog:view}),Committed:committed,Host:core.HostOf(scope),PlannerCosts:plan.PlannerCosts{Tuple:1},PlannerRows:float64(len(rows)),PlannerColumns:1}
		out:=append(append(plan.QuerySchema(nil),schema...),catalog.Column{Name:"count",Usage:bigint.Usage()})
		aggregate:=&plan.AggregateNode{QuerySchema:out,Child:leaf,Keys:[]expr.Expr{column},Grouping:[]plan.Grouping{{Grouping:grouping}},Calls:[]plan.AggregateCall{{Provider:functions.NewCountProvider(bigint.Usage())}}}
		project:=&plan.ProjectNode{QuerySchema:out,Child:aggregate,Frame:1,Expressions:[]expr.Expr{column,plannerColumn(bigint,1)}}
		ctx:=&plannerExecutionContext{Context:context.Background()}
		result,n,err:=exec.FinalizeSelect(ctx,project,source,nil,2)
		if err!=nil || n!=3 { t.Fatal(n,err) }
		if disabled && (ctx.partial.Load() || ctx.hash.Load()) { t.Fatal("disabled hashagg reached partial hashing") }
		result.Close()
	}
}
