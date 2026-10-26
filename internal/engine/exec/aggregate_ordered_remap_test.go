package exec

import (
 "context"
 "fmt"
 "testing"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/routine"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqltype"
)

func TestAggregateOrderedFrameRemap(t *testing.T){
 source,destination:=map[sqltype.ID]*sqltype.Descriptor{},map[sqltype.ID]*sqltype.Descriptor{}
 for _,typ:=range sqltype.Builtins(){source[typ.ID()]=typ}
 for _,typ:=range sqltype.Builtins(){destination[typ.ID()]=typ}
 var forward,final *catalog.Procedure
 declarations:=routine.BuiltinProcedures(authorization.PrincipalRef{})
 for i:=range declarations{d:=&declarations[i];switch d.Address().ObjectID{case 3971:forward=d;case 3987:final=d}}
 if forward==nil||final==nil{t.Fatal("missing actual support entrypoints")}
 rows:=[]sqltype.Usage{source[sqltype.Int4].Usage(),source[sqltype.Text].Usage()}
 keys:=make([]functions.AggregateSortKey,2);order:=make([]plan.SortKey,2)
 for i,u:=range rows{
  comparison,err:=sqltype.BindOrdering(u);if err!=nil{t.Fatal(err)}
  key:=parser.OrderKey{Desc:i==1,Nulls:parser.NullsLast}
  keys[i]=functions.AggregateSortKey{Comparison:comparison,Key:key,Argument:true}
  order[i]=plan.SortKey{Comparison:comparison,Desc:key.Desc,Slot:i,Cache:i}
 }
 aggregate:=&catalog.Procedure{ObjectAddress:ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:94001},ObjectName:"different_user_name",RoutineKind:catalog.AggregateRoutine,Signature:"2276",Result:20,Aggregate:&catalog.OwnedAggregate{Variadic:true,Definition:catalog.AggregateDefinition{Function:94001,Kind:"h",DirectArgs:1,Functions:[8]ids.OID{3971,3987},TransitionType:2281,FinalExtra:true,FinalModify:"w",MovingFinalModify:"r"}}}
 stateUsage,result:=source[2281].Usage(),source[sqltype.Int8].Usage()
 recipe,err:=functions.NewAggregateContextSupport(3971,3987,true,true,result,rows,rows,keys);if err!=nil{t.Fatal(err)}
 inputs:=append([]sqltype.Usage{stateUsage},rows...)
 finalInputs:=append(append([]sqltype.Usage{stateUsage},rows...),rows...)
 provider:=&TransitionAggregate{Aggregate:aggregate,Support:forward,Final:final,StateUsage:stateUsage,Result:result,FinalModify:"w",Inputs:inputs,Formals:append([]sqltype.Usage(nil),inputs...),DirectInputs:append([]sqltype.Usage(nil),rows...),FinalInputs:finalInputs,FinalFormals:append([]sqltype.Usage(nil),finalInputs...),ContextSupport:recipe,ContextOrder:order}
 mapped,err:=provider.Remap(functions.ProviderRemap{
  Type:func(old *sqltype.Descriptor)*sqltype.Descriptor{if old!=source[old.ID()]{t.Fatal("type mapped twice")};return destination[old.ID()]},
  Usage:func(old sqltype.Usage)(sqltype.Usage,error){if old.Type!=source[old.Type.ID()]{return sqltype.Usage{},fmt.Errorf("usage mapped twice")};old.Type=destination[old.Type.ID()];return old,nil},
  Routine:func(reference expr.RoutineReference)(expr.RoutineReference,error){return reference,nil},
  Expression:func(expr.Expr,[]sqltype.Usage,[]sqltype.Usage)(expr.Expr,error){t.Fatal("opaque state retained a scalar/private query frame");return nil,nil},
 });if err!=nil{t.Fatal(err)}
 next:=mapped.(*TransitionAggregate)
 if provider.Inputs[1].Type!=source[sqltype.Int4]||provider.ContextSupport!=recipe||next.ContextSupport==recipe||next.StateUsage.Type!=destination[2281]||next.Result.Type!=destination[sqltype.Int8]{t.Fatal("source mutation or stale state/result generation")}
 for _,vector:=range [][]sqltype.Usage{next.Inputs,next.DirectInputs,next.FinalInputs,next.ContextSupport.Rows(),next.ContextSupport.Direct()}{for _,usage:=range vector{if usage.Type!=destination[usage.Type.ID()]{t.Fatal("retained source usage",usage)}}}
 ctx:=context.Background();evaluation:=&Context{EvalStatement:EvalStatement{Context:ctx}}
 state,err:=next.InitWithContext(ctx,evaluation);if err!=nil{t.Fatal(err)}
 oldNumber,_:=sqltype.Integer(source[sqltype.Int4],1)
 if err=next.Advance(ctx,state,[]sqltype.Value{oldNumber,sqltype.String(source[sqltype.Text],"a")},evaluation);err==nil{t.Fatal("foreign-generation row admitted")}
 for _,tuple:=range []struct{n int64;text string}{{1,"a"},{1,"b"},{2,"a"}}{number,_:=sqltype.Integer(destination[sqltype.Int4],tuple.n);if err=next.Advance(ctx,state,[]sqltype.Value{number,sqltype.String(destination[sqltype.Text],tuple.text)},evaluation);err!=nil{t.Fatal(err)}}
 target,_:=sqltype.Integer(destination[sqltype.Int4],1)
 value,err:=next.FinalizeWithContext(ctx,state,[]sqltype.Value{target,sqltype.String(destination[sqltype.Text],"b")},evaluation);if err!=nil{t.Fatal(err)}
 got,_:=value.Integer();if got!=1||value.Type()!=destination[sqltype.Int8]{t.Fatal("remapped DESC second-key rank",got,value.Type())}
 bad:=*next;bad.DirectInputs=append([]sqltype.Usage(nil),next.DirectInputs...);bad.DirectInputs[0]=source[sqltype.Int4].Usage()
 if err=bad.ValidateSelection();err==nil{t.Fatal("same-shape foreign direct specialization admitted")}
}
