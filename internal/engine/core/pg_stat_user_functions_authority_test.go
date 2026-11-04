package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPGStatUserFunctionsAuthority(t *testing.T) {
	t.Run("proof_ownership",func(t *testing.T){
		e:=engine.New();read:=e.CatalogRead();d:=read.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatUserFunctionsOID});ref:=read.Owner().Domain.Ref(read.Epoch(),d.Address());boolean,_:=read.LookupType(sqltype.Bool)
		leaf:=exec.Leaf{Ref:ref,Relation:d,Table:&parser.TableReference{},Uses:[]int{2,4}}
		scan:=&plan.SourceNode{Ref:ref};parameter:=plan.ParameterExpr{Info:expr.Info{Usage:boolean.Usage()},Number:1}
		origin:=ref;origin.Address.SubID=d.Attributes[4];column:=plan.ColumnExpr{Info:expr.Info{Usage:d.Columns[4].Usage},Origin:origin,Scope:1,Frame:1,Slot:4}
		filter:=&plan.FilterNode{Child:scan,Predicate:plan.LogicalExpr{Op:parser.LogicalAnd,Left:plan.ColumnExpr{},Right:parameter}}
		project:=&plan.ProjectNode{Frame:1,Child:filter,Expressions:[]expr.Expr{column}}
		query:=catalogrt.KeepQuery{Root:project,Leaves:[]exec.Leaf{leaf}}
		proof:=func(leaves []exec.Leaf,mark plan.RowMark,locks *exec.LockContext)(catalogrt.FunctionStatsDemand,error){return query.FunctionStats(t.Context(),sqltype.InputEnvironment{},sqltype.OutputEnvironment{},leaves,0,mark,locks)}
		for _,value:=range []sqltype.Value{sqltype.Boolean(boolean,true),sqltype.Boolean(boolean,false),sqltype.Null(boolean),sqltype.Boolean(boolean,true)}{query.Parameters=[]sqltype.Value{value};got,err:=proof([]exec.Leaf{leaf},plan.RowMark{},nil);truth,_:=plan.TruthOf(value);want:=catalogrt.FunctionStatsDemand{Getters:2};if truth==plan.True{want=catalogrt.FunctionStatsDemand{Getters:3,ReadInput:true}};if err!=nil||got!=want{t.Fatal("current parameter and surviving target",value,got,want,err)}}
		query.Parameters=[]sqltype.Value{sqltype.Boolean(boolean,false)}
		foreign:=leaf;foreign.Table=&parser.TableReference{};if got,err:=proof([]exec.Leaf{foreign},plan.RowMark{},nil);err!=nil||!got.ReadInput||got.Getters!=3{t.Fatal("foreign query borrowed proof",got,err)}
		foreign=leaf;foreign.Ref.Epoch++;if got,err:=proof([]exec.Leaf{foreign},plan.RowMark{},nil);err!=nil||!got.ReadInput{t.Fatal("foreign epoch borrowed proof",got,err)}
		if got,err:=proof([]exec.Leaf{leaf},plan.RowMark{},&exec.LockContext{});err!=nil||!got.ReadInput{t.Fatal("locked source borrowed proof",got,err)}
		project.Frame=2;if got,err:=proof([]exec.Leaf{leaf},plan.RowMark{},nil);err!=nil||!got.ReadInput{t.Fatal("foreign frame borrowed proof",got,err)};project.Frame=1
		query.Root=&plan.FilterNode{Child:project,Predicate:parameter};if got,err:=proof([]exec.Leaf{leaf},plan.RowMark{},nil);err!=nil||!got.ReadInput{t.Fatal("unsupported topology guessed empty",got,err)};query.Root=project
		query.Parameters=nil;if _,err:=proof([]exec.Leaf{leaf},plan.RowMark{},nil);err==nil{t.Fatal("failed parameter proof swallowed")}
		ctx,cancel:=context.WithCancel(t.Context());cancel();if _,err:=query.FunctionStats(ctx,sqltype.InputEnvironment{},sqltype.OutputEnvironment{},[]exec.Leaf{leaf},0,plan.RowMark{},nil);!errors.Is(err,context.Canceled){t.Fatal("canceled proof",err)}
	})
	t.Run("transparent_derived_ownership",func(t *testing.T){
		e:=engine.New();read:=e.CatalogRead();d:=read.MustRelation(ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatUserFunctionsOID});ref:=read.Owner().Domain.Ref(read.Epoch(),d.Address());boolean,_:=read.LookupType(sqltype.Bool)
		syntax:=&parser.Select{};leaf:=exec.Leaf{Ref:ref,Relation:d,Table:&parser.TableReference{},Query:syntax,Uses:[]int{0}}
		origin:=ref;origin.Address.SubID=d.Attributes[0]
		identity:=plan.ColumnExpr{Info:expr.Info{Usage:d.Columns[0].Usage},Origin:origin,Scope:1,Frame:2,Slot:0}
		source:=&plan.SourceNode{Ref:ref}
		inner:=&plan.ProjectNode{Frame:2,Syntax:syntax,Expressions:[]expr.Expr{identity},Child:source}
		derived:=&plan.DerivedNode{QuerySchema:plan.QuerySchema{d.Columns[0]},Query:&plan.Query{Root:inner},ScanFrame:1}
		outer:=plan.ColumnExpr{Info:expr.Info{Usage:d.Columns[0].Usage},Scope:1,Frame:1,Slot:0}
		root:=&plan.ProjectNode{Frame:1,Expressions:[]expr.Expr{outer},Child:&plan.FilterNode{Child:derived,Predicate:plan.LiteralExpr{Info:expr.Info{Usage:boolean.Usage()},Value:sqltype.Boolean(boolean,false)}}}
		q:=catalogrt.KeepQuery{Root:root,Leaves:[]exec.Leaf{leaf}}
		proof:=func()catalogrt.FunctionStatsDemand{got,err:=q.FunctionStats(t.Context(),sqltype.InputEnvironment{},sqltype.OutputEnvironment{},[]exec.Leaf{leaf},0,plan.RowMark{},nil);if err!=nil{t.Fatal(err)};return got}
		if got:=proof();got!=(catalogrt.FunctionStatsDemand{}){t.Fatal("transparent identity not eliminated",got)}
		inner.Syntax=&parser.Select{};if got:=proof();!got.ReadInput{t.Fatal("foreign nested syntax borrowed proof",got)};inner.Syntax=syntax
		source.Ref.Epoch++;if got:=proof();!got.ReadInput{t.Fatal("foreign nested ref borrowed proof",got)};source.Ref=ref
		identity.Frame=1;inner.Expressions=[]expr.Expr{identity};if got:=proof();!got.ReadInput{t.Fatal("outer frame applied to inner leaf",got)};identity.Frame=2
		identity.Slot=3;inner.Expressions=[]expr.Expr{identity};if got:=proof();!got.ReadInput{t.Fatal("getter-derived output guessed transparent",got)}
		identity.Slot=0;inner.Expressions=[]expr.Expr{identity}
		source.Occurrence=1;if got:=proof();!got.ReadInput{t.Fatal("outer index borrowed nested occurrence",got)};source.Occurrence=0
		outer.Slot=1;root.Expressions=[]expr.Expr{outer};if got:=proof();!got.ReadInput{t.Fatal("invalid derived slot bypassed authority",got)};outer.Slot=0;root.Expressions=[]expr.Expr{outer}
		identity.Virtual=plan.LiteralExpr{Info:expr.Info{Usage:d.Columns[0].Usage}};inner.Expressions=[]expr.Expr{identity};if got:=proof();!got.ReadInput{t.Fatal("virtual target guessed identity",got)};identity.Virtual=nil;inner.Expressions=[]expr.Expr{identity}
		derived.CTE=new(plan.CTEDefinition);if got:=proof();!got.ReadInput{t.Fatal("CTE boundary borrowed derived proof",got)};derived.CTE=nil
		for _,flag:=range []*bool{&inner.RuleInvocation,&inner.CTEOwner,&inner.HasTargetSet,&root.RuleInvocation,&root.CTEOwner,&root.HasTargetSet}{*flag=true;if got:=proof();!got.ReadInput{t.Fatal("invocation boundary bypassed authority",got)};*flag=false}
		inner.Child=&plan.FilterNode{Child:source};if got:=proof();!got.ReadInput{t.Fatal("unsupported nested filter guessed transparent",got)};inner.Child=source
	})
	for _,getter:=range []string{"pg_stat_get_function_calls","pg_stat_get_function_total_time","pg_stat_get_function_self_time"}{t.Run(getter,func(t *testing.T){
		e:=engine.New();admin:=enginetest.StartSession(t,e,nil);s:=enginetest.StartSession(t,e,nil)
		statsFunctionExec(t,admin,"CREATE ROLE uf_authority");statsFunctionExec(t,s,"SET ROLE uf_authority","SET stats_fetch_consistency=snapshot")
		ast,description:=enginetest.PreparedDescription(t,e,s,"SELECT * FROM pg_stat_user_functions")
		statsFunctionExec(t,admin,"REVOKE EXECUTE ON FUNCTION "+getter+"(oid) FROM PUBLIC")
		result,err:=enginetest.PreparedExecute(e,s,ast,&description);if result.Rows!=nil{result.Rows.Close();t.Fatal("denial published source")};enginetest.Diagnostic(t,err,sqlstate.Error{Code:"42501",Message:"permission denied for function "+getter})
		owned:=reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader");if owned.FieldByName("full").Bool()||owned.FieldByName("cache").Len()!=0{t.Fatal("denial configured cumulative cut")}
		statsFunctionExec(t,s,"SELECT funcid FROM pg_stat_user_functions WHERE FALSE","SELECT count(*) FROM pg_stat_user_functions WHERE FALSE")
		if owned.FieldByName("full").Bool()||owned.FieldByName("cache").Len()!=0{t.Fatal("eliminated source populated cut")}
		op:=enginetest.TransactionOperation(t,s);scope,err:=e.StartStatement(op);if err!=nil{t.Fatal(err)}
		read:=e.CatalogRead();a:=ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:pgcatalog.StatUserFunctionsOID};source:=read.MustRelation(a);ref:=e.Catalog.Domain.Ref(read.Epoch(),a);e.Mu.Lock()
		captured,err:=core.EngineOpenCatalogSourceLocked(e,scope,ref,source,nil);if captured!=nil{captured.Close();t.Fatal("low-level bypass published source")};enginetest.Diagnostic(t,err,sqlstate.Error{Code:"42501",Message:"permission denied for function "+getter});scope.Finish(nil);op.Done()
		statsFunctionExec(t,admin,"GRANT EXECUTE ON FUNCTION "+getter+"(oid) TO PUBLIC");result,err=enginetest.PreparedExecute(e,s,ast,&description);if err!=nil||result.Rows==nil{t.Fatal("held current regrant",err)};result.Rows.Close()
	})}
}
