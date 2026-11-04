package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPGStatXactUserFunctionsAuthority(t *testing.T) {
	t.Run("proof_ownership",func(t *testing.T){
		e:=engine.New();read:=e.CatalogRead();d:=read.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatXactUserFunctionsOID});ref:=read.Owner().Domain.Ref(read.Epoch(),d.Address())
		boolean,_:=read.LookupType(sqltype.Bool);literal:=plan.LiteralExpr{Info:expr.Info{Usage:boolean.Usage()},Value:sqltype.Boolean(boolean,false)}
		scan:=&plan.SourceNode{Ref:ref};root:=&plan.FilterNode{Child:scan,Predicate:literal}
		leaf:=exec.Leaf{Ref:ref,Relation:d,Table:&parser.TableReference{}}
		query:=catalogrt.KeepQuery{Root:root,Leaves:[]exec.Leaf{leaf}}
		if !query.ScalarStatsReadSuppressed([]exec.Leaf{leaf},0,plan.RowMark{}){t.Fatal("owned literal false proof lost")}
		foreign:=leaf;foreign.Table=&parser.TableReference{}
		if query.ScalarStatsReadSuppressed([]exec.Leaf{foreign},0,plan.RowMark{}){t.Fatal("proof inherited by foreign query occurrence")}
		foreign=leaf;foreign.Ref.Epoch++
		if query.ScalarStatsReadSuppressed([]exec.Leaf{foreign},0,plan.RowMark{}){t.Fatal("proof inherited by foreign catalog incarnation")}
		query.Root=&plan.FilterNode{Child:&plan.ProjectNode{Child:scan},Predicate:literal}
		if query.ScalarStatsReadSuppressed([]exec.Leaf{leaf},0,plan.RowMark{}){t.Fatal("outer proof crossed nested projection boundary")}
		parameter:=plan.ParameterExpr{Info:expr.Info{Usage:boolean.Usage()},Number:1}
		query.Root=&plan.ProjectNode{Frame:1,Child:&plan.FilterNode{Child:scan,Predicate:parameter}}
		for _,value:=range []sqltype.Value{sqltype.Boolean(boolean,false),sqltype.Boolean(boolean,true),sqltype.Boolean(boolean,false),sqltype.Null(boolean)}{
			query.Parameters=[]sqltype.Value{value}
			rejects,err:=query.RejectsInput(t.Context(),sqltype.InputEnvironment{},sqltype.OutputEnvironment{},[]exec.Leaf{leaf},0,plan.RowMark{},nil)
			truth,_:=plan.TruthOf(value);if err!=nil||rejects!=(truth!=plan.True){t.Fatal("current bound predicate proof",value,rejects,err)}
			if query.ScalarStatsReadSuppressed([]exec.Leaf{leaf},0,plan.RowMark{}){t.Fatal("parameter widened literal-only scalar proof")}
		}
		foreign=leaf;foreign.Table=&parser.TableReference{}
		if rejects,err:=query.RejectsInput(t.Context(),sqltype.InputEnvironment{},sqltype.OutputEnvironment{},[]exec.Leaf{foreign},0,plan.RowMark{},nil);err!=nil||rejects{t.Fatal("parameter proof crossed occurrence",rejects,err)}
		query.Parameters=nil
		if _,err:=query.RejectsInput(t.Context(),sqltype.InputEnvironment{},sqltype.OutputEnvironment{},[]exec.Leaf{leaf},0,plan.RowMark{},nil);err==nil{t.Fatal("missing imported parameter suppressed as empty")}
	})
	for _,getter:=range []string{"pg_stat_get_xact_function_calls","pg_stat_get_xact_function_total_time","pg_stat_get_xact_function_self_time"}{t.Run(getter,func(t *testing.T){
		e:=engine.New();admin:=enginetest.StartSession(t,e,nil);s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,admin,"CREATE ROLE xf_authority")
		queries:=[]string{"SELECT funcid FROM pg_stat_xact_user_functions","SELECT calls FROM pg_stat_xact_user_functions","SELECT count(*) FROM pg_stat_xact_user_functions","SELECT total_time FROM pg_stat_xact_user_functions LIMIT 0","SELECT funcid FROM pg_stat_xact_user_functions WHERE TRUE"}
		statsFunctionExec(t,s,"SET ROLE xf_authority","SET stats_fetch_consistency=snapshot")
		for _,q:=range queries{
			ast,description:=enginetest.PreparedDescription(t,e,s,q)
			falseAST,falseDescription:=enginetest.PreparedDescription(t,e,s,"SELECT funcid FROM pg_stat_xact_user_functions WHERE FALSE")
			statsFunctionExec(t,admin,"REVOKE EXECUTE ON FUNCTION "+getter+"(oid) FROM PUBLIC")
			result,err:=enginetest.PreparedExecute(e,s,ast,&description);if result.Rows!=nil{result.Rows.Close();t.Fatal("denial published source")}
			enginetest.Diagnostic(t,err,sqlstate.Error{Code:"42501",Message:"permission denied for function "+getter})
			statsFunctionExec(t,s,"SELECT funcid FROM pg_stat_xact_user_functions WHERE FALSE","SELECT count(*) FROM pg_stat_xact_user_functions WHERE FALSE")
			falseResult,err:=enginetest.PreparedExecute(e,s,falseAST,&falseDescription);if err!=nil||falseResult.Rows==nil{t.Fatal("held literal false admission",err)}
			var cell [1]sqltype.Value;present,err:=falseResult.Rows.Next(t.Context(),cell[:]);falseResult.Rows.Close();if err!=nil||present{t.Fatal("held literal false rows",present,err)}
			owned:=reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
			if owned.FieldByName("full").Bool()||owned.FieldByName("kinds").Uint()!=0{t.Fatal("xact authority populated cumulative Reader")}
			statsFunctionExec(t,admin,"GRANT EXECUTE ON FUNCTION "+getter+"(oid) TO PUBLIC")
			result,err=enginetest.PreparedExecute(e,s,ast,&description);if err!=nil||result.Rows==nil{t.Fatal("held regrant",err)};result.Rows.Close()
		}
		statsFunctionExec(t,admin,"REVOKE EXECUTE ON FUNCTION "+getter+"(oid) FROM PUBLIC")
		op:=enginetest.TransactionOperation(t,s);defer op.Done();scope,err:=e.StartStatement(op);if err!=nil{t.Fatal(err)};defer scope.Finish(nil)
		read:=e.CatalogRead();a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatXactUserFunctionsOID};source:=read.MustRelation(a);ref:=e.Catalog.Domain.Ref(read.Epoch(),a);e.Mu.Lock()
		captured,err:=core.EngineOpenCatalogSourceLocked(e,scope,ref,source,[]int{})
		if captured!=nil{captured.Close();t.Fatal("low-level bypass published source")};enginetest.Diagnostic(t,err,sqlstate.Error{Code:"42501",Message:"permission denied for function "+getter})
	})}
}

// A real canceled outer completes no own logical event, but its successful
// recursive descendant remains reportable; incumbent elapsed proof stays exact.
func TestPGStatXactUserFunctionsCanceledLogicalTiming(t *testing.T) {
	synctest.Test(t,func(t *testing.T){
		e:=engine.New();s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,s,"CREATE FUNCTION xf_cancel(n integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF n=0 THEN PERFORM xf_cancel(1); PERFORM pg_sleep(0.100); END IF; RETURN 7; END$$","CREATE FUNCTION xf_next() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN 7; END$$","BEGIN","SET track_functions=pl")
		key,next:=statsFunctionKey(t,e,"xf_cancel"),statsFunctionKey(t,e,"xf_next")
		ctx,cancel:=context.WithCancel(context.Background());defer cancel();op,err:=s.Acquire(ctx);if err!=nil{t.Fatal(err)}
		joined:=make(chan struct{});go func(){defer close(joined);time.Sleep(8*time.Millisecond);cancel()}()
		result,err:=e.Execute(op,enginetest.Statement(t,"SELECT xf_cancel(0)"),0,nil);if result.Rows!=nil{result.Rows.Close()};op.Done();<-joined
		if !errors.Is(err,context.Canceled){t.Fatal("actual canceled body",err)}
		statsFunctionExec(t,s,"ROLLBACK","BEGIN","SET track_functions=pl","SELECT xf_next()")
		statsFunctionRetire(t,s)
		f,g:=e.Statistics.Read(key),e.Statistics.Read(next)
		if f.Calls!=1||f.FunctionLogicalTotal!=1||f.FunctionLogicalSelf!=1||g.Calls!=1||g.FunctionLogicalTotal!=1||g.FunctionLogicalSelf!=1{t.Fatal("canceled outer counted or leaked baseline",f,g)}
	})
}
