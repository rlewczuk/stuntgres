package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestExplainAnalyzePreparedOwnership(t *testing.T) {
	t.Run("protocol", explainAnalyzeProtocolOwnership)
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	enginetest.SessionSQL(t,s,"CREATE TABLE ea_owner(v integer)")
	enginetest.SessionSQL(t,s,"INSERT INTO ea_owner VALUES(1)")
	ast,description:=sequencePreparedDescription(t,e,s,"EXPLAIN ANALYZE UPDATE ea_owner SET v=v+$1 RETURNING v")
	if len(description.Results)!=1 || description.Results[0].Name!="QUERY PLAN" || description.Results[0].Usage.Type.ID()!=sqltype.Text || description.Parameters.Count!=1 {t.Fatal(description)}
	resultBacking,parameterBacking:=&description.Results[0],&description.Parameters.Types[0]
	foldOwner:=description.Folds
	for _,value:=range []int64{2,4,3} {
		if value==3 {
			enginetest.SessionSQL(t,s,"ALTER TABLE ea_owner ADD COLUMN extra integer DEFAULT 9")
		}
		op:=enginetest.TransactionOperation(t,s)
		scope,err:=e.StartStatement(op)
		if err!=nil {t.Fatal(err)}
		scope.Expected=&description
		imports:=int64(0)
		result,err:=scope.Execute(ast,1,func(ctx context.Context,parameters analyze.ParameterInfo,frame []sqltype.Value,input sqltype.InputEnvironment)error{
			imports++
			frame[0],err=sqltype.Integer(parameters.Types[0].Type,value*imports)
			return err
		})
		if err!=nil {scope.Finish(err);t.Fatal(err)}
		if result.Count!=0 || result.CommandTag!="EXPLAIN" || result.Rows==nil {t.Fatal(result)}
		if core.ExplainActiveForTest(scope) {t.Fatal("execution retained metrics or command graphs")}
		if err:=scope.Finish(nil);err!=nil {t.Fatal(err)}
		result.Rows.Close()
		if imports!=1 || scope.Expected!=&description || &description.Results[0]!=resultBacking || &description.Parameters.Types[0]!=parameterBacking || description.Folds!=foldOwner || description.FoldPlanning!=nil {t.Fatal("child changed retained wrapper ownership")}
		if foldOwner!=nil {
			accepted:=foldOwner.Current()
			if accepted.Generation!=0 || len(accepted.Entries)!=0 {t.Fatal("wrapper gained child fold publication",accepted)}
		}
	}
	enginetest.TransactionRows(t,s,"SELECT v FROM ea_owner",10)
	failedSyntax,failedDescription:=sequencePreparedDescription(t,e,s,"EXPLAIN ANALYZE SELECT 1/(v-v) FROM ea_owner")
	failedOwner:=failedDescription.Folds
	result,failure:=sequencePreparedExecute(e,s,failedSyntax,&failedDescription)
	enginetest.State(t,failure,"22012")
	if result.Rows!=nil || failedDescription.Folds!=failedOwner || failedDescription.FoldPlanning!=nil {t.Fatal("failed child published output or replaced definition")}
	if failedOwner!=nil && (failedOwner.Current().Generation!=0 || len(failedOwner.Current().Entries)!=0) {t.Fatal("failed child accepted a retained fold generation")}
	enginetest.LockEmpty(t,e)
}

func TestExplainAnalyzeExecutionBoundary(t *testing.T) {
	t.Run("native-demand",func(t *testing.T){
		for _,c:=range []struct{query string;rows,sourceRows,sourceLoops int64}{
			{"SELECT id FROM t LIMIT 0",0,0,0},
			{"SELECT id FROM t LIMIT 1",1,1,1},
			{"SELECT id FROM t ORDER BY id DESC LIMIT 1",1,4,1},
		}{
			t.Run(c.query,func(t *testing.T){
				_,query,source:=queryCoreFixture(t,c.query)
				ctx,observed:=exec.Observe(context.Background(),true)
				observed.Register(query.Root)
				rows,count,err:=exec.Finalize(ctx,query.Root,source,nil)
				if err!=nil {t.Fatal(err)}
				rows.Close()
				if int64(count)!=c.rows {t.Fatal(count)}
				if actual:=observed.Actual(query.Root);actual.Rows!=c.rows || actual.Loops!=1 {t.Fatal("root output",actual)}
				node:=query.Root
				for {
					if _,scan:=node.(*plan.SourceNode);scan {break}
					node,_=plan.QueryInputs(node)
					if node==nil {t.Fatal("fixture lacks real scan")}
				}
				if actual:=observed.Actual(node);actual.Rows!=c.sourceRows || actual.Loops!=c.sourceLoops {t.Fatal("demand differs from source capacity",actual,c)}
			})
		}
	})
	t.Run("parallel-runtime-records",func(t *testing.T){
		_,query,source:=queryCoreFixture(t,"SELECT id FROM t")
		pin,err:=exec.ParallelQuerySource(source)
		if err!=nil {t.Fatal(err)}
		ctx,observed:=exec.Observe(context.Background(),true)
		observed.Register(query.Root)
		rows,count,err:=exec.FinalizeSelect(ctx,query.Root,pin,nil,2)
		if err!=nil {t.Fatal(err)}
		rows.Close()
		if count!=4 {t.Fatal(count)}
		if actual:=observed.Actual(query.Root);actual.Rows!=4 || actual.Loops!=2 || actual.Elapsed<0 {t.Fatal("actual parallel workers were not independently merged",actual)}
	})
	t.Run("publication-veto",func(t *testing.T) {
		e:=engine.New()
		s:=enginetest.StartSession(t,e,nil)
		enginetest.SessionSQL(t,s,"CREATE TABLE ea_boundary(v integer)")
		enginetest.SessionSQL(t,s,"INSERT INTO ea_boundary VALUES(1),(2)")
		op:=enginetest.TransactionOperation(t,s)
		scope,err:=e.StartStatement(op)
		if err!=nil {t.Fatal(err)}
		result,err:=scope.Execute(enginetest.Statement(t,"EXPLAIN ANALYZE UPDATE ea_boundary SET v=v+10 RETURNING v"),0,nil)
		if err!=nil {scope.Finish(err);t.Fatal(err)}
		defer result.Rows.Close()
		if len(scope.Stage.Rows)==0 || scope.Completed || core.ExplainActiveForTest(scope) {t.Fatal("child bypassed ordinary private publication or retained execution")}
		base,cancel:=context.WithCancel(op.Context())
		defer cancel()
		op.Ctx=&cancelAtErrCheck{Context:base,cancel:cancel,at:2}
		if err:=scope.Finish(nil);!errors.Is(err,context.Canceled) {t.Fatal(err)}
		result.Rows.Close()
		if !scope.Finished || scope.View!=nil || scope.LockTicket!=nil || core.ExplainActiveForTest(scope) {t.Fatal("failed Finish retained execution owners")}
		enginetest.TransactionRows(t,s,"SELECT v FROM ea_boundary ORDER BY v",1,2)
		enginetest.LockEmpty(t,e)
	})
	t.Run("runtime-error",func(t *testing.T) {
		e:=engine.New()
		s:=enginetest.StartSession(t,e,nil)
		enginetest.SessionSQL(t,s,"CREATE TABLE ea_boundary(v integer)")
		enginetest.SessionSQL(t,s,"INSERT INTO ea_boundary VALUES(1),(2)")
		op:=enginetest.TransactionOperation(t,s)
		scope,err:=e.StartStatement(op)
		if err!=nil {t.Fatal(err)}
		result,err:=scope.Execute(enginetest.Statement(t,"EXPLAIN ANALYZE UPDATE ea_boundary SET v=v+10 RETURNING 1/(v-v)"),0,nil)
		enginetest.State(t,err,"22012")
		if result.Rows!=nil || result.Count!=0 || core.ExplainActiveForTest(scope) {t.Fatal("error exposed plan or retained owners",result)}
		if err:=scope.Finish(err);err==nil {t.Fatal("failed execution became success")}
		enginetest.TransactionRows(t,s,"SELECT v FROM ea_boundary ORDER BY v",1,2)
		enginetest.LockEmpty(t,e)
	})
}
