package exec

import (
 "context"
 "errors"
 "fmt"
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/sqltype"
)

func orderedUnitProvider(t *testing.T) (*TransitionAggregate,*sqltype.Descriptor,*sqltype.Descriptor) {
 t.Helper()
 p,text:=finalUnitProvider(t)
 integer:=p.StateUsage.Type
 p.Aggregate.Aggregate.Definition.Kind="o"
 p.Aggregate.Aggregate.Definition.DirectArgs=1
 p.DirectInputs=[]sqltype.Usage{text.Usage()}
 p.FinalInputs=[]sqltype.Usage{integer.Usage(),text.Usage(),integer.Usage(),text.Usage()}
 p.FinalFormals=append([]sqltype.Usage(nil),p.FinalInputs...)
 p.FinalConversions=make([]sqltype.CoercionPlan,len(p.FinalInputs))
 for i,u:=range p.FinalInputs{var err error;p.FinalConversions[i],err=sqltype.PlanCoercion(u,u,sqltype.Implicit);if err!=nil{t.Fatal(err)}}
 p.FinalCall=transitionUnitExpr{Info:expr.Info{Usage:text.Usage()},run:func(c expr.Context)(sqltype.Value,error){
  values:=c.(*Context).Parameters
  if len(values)!=4||values[0].Type()!=integer||values[1].Type()!=text||values[2].Type()!=integer||values[3].Type()!=text||!values[2].IsNull()||!values[3].IsNull(){return sqltype.Value{},fmt.Errorf("invalid ordered final vector: %v",values)}
  total,err:=values[0].Integer();if err!=nil{return sqltype.Value{},err}
  prefix,err:=values[1].Text();if err!=nil{return sqltype.Value{},err}
  return sqltype.String(text,fmt.Sprintf("%s%d",prefix,total)),nil
 }}
 return p,integer,text
}

func TestAggregateOrderedVectors(t *testing.T){
 p,integer,text:=orderedUnitProvider(t)
 ctx:=context.Background()
 outer:=sqltype.String(text,"outer")
 evaluation:=&Context{EvalStatement:EvalStatement{Context:ctx,Parameters:[]sqltype.Value{outer}}}
 state,err:=p.InitWithContext(ctx,evaluation);if err!=nil{t.Fatal(err)}
 for _,n:=range []int64{3,1,2}{v,_:=sqltype.Integer(integer,n);if err=p.Advance(ctx,state,[]sqltype.Value{v,sqltype.String(text,"row")},evaluation);err!=nil{t.Fatal(err)}}
 before:=state.(*transitionAggregateState).value
 value,err:=p.FinalizeWithContext(ctx,state,[]sqltype.Value{sqltype.String(text,"p:")},evaluation)
 if err!=nil{t.Fatal(err)}
 got,_:=value.Text();if got!="p:6"{t.Fatal("final did not receive state/direct/typed NULL extras",got)}
 if state.(*transitionAggregateState).value!=before||evaluation.Parameters[0]!=outer{t.Fatal("final overwrote transition or enclosing parameters")}
 if _,err=p.FinalizeWithContext(ctx,state,nil,evaluation);err==nil{t.Fatal("missing direct vector admitted")}
}

func TestAggregateOrderedDirectFrame(t *testing.T){
 p,_,text:=orderedUnitProvider(t)
 ctx:=context.Background()
 evaluation:=&Context{EvalStatement:EvalStatement{Context:ctx}}
 states:=make([]any,2)
 for i:=range states{var err error;states[i],err=p.InitWithContext(ctx,evaluation);if err!=nil{t.Fatal(err)}}
 for _,tc:=range []struct{group int;prefix,want string}{{0,"a:","a:0"},{1,"b:","b:0"},{0,"again:","again:0"}}{
  value,err:=p.FinalizeWithContext(ctx,states[tc.group],[]sqltype.Value{sqltype.String(text,tc.prefix)},evaluation);if err!=nil{t.Fatal(err)}
  got,_:=value.Text();if got!=tc.want{t.Fatalf("group %d got %q want %q",tc.group,got,tc.want)}
 }
 if states[0].(*transitionAggregateState).finalFrame==states[1].(*transitionAggregateState).finalFrame{t.Fatal("groups shared mutable final frame")}
 p.Final.Strict=true
	p.Aggregate.Aggregate.Definition.FinalExtra=false
 p.FinalInputs=p.FinalInputs[:2];p.FinalFormals=p.FinalFormals[:2];p.FinalConversions=p.FinalConversions[:2]
 p.FinalCall=transitionUnitExpr{Info:expr.Info{Usage:text.Usage()},run:func(expr.Context)(sqltype.Value,error){t.Fatal("strict NULL direct reached final");return sqltype.Value{},nil}}
 state,err:=p.InitWithContext(ctx,evaluation);if err!=nil{t.Fatal(err)}
 value,err:=p.FinalizeWithContext(ctx,state,[]sqltype.Value{sqltype.Null(text)},evaluation)
 if err!=nil||!value.IsNull()||value.Type()!=text{t.Fatal("strict final did not suppress NULL direct",value,err)}
}

func TestAggregateOrderedStreaming(t *testing.T){
 p,integer,text:=orderedUnitProvider(t)
 if !ownedOrderedAggregate(p){t.Fatal("ordered user support routed through executor sorting")}
 p.Call=transitionUnitExpr{Info:expr.Info{Usage:integer.Usage()},run:func(c expr.Context)(sqltype.Value,error){
  values:=c.(*Context).Parameters
  if len(values)!=3{t.Fatal("direct operand reached SFUNC",values)}
  state,_:=values[0].Integer();row,_:=values[1].Integer()
  return sqltype.Integer(integer,state*10+row)
 }}
 ctx:=context.Background();evaluation:=&Context{EvalStatement:EvalStatement{Context:ctx}}
 state,err:=p.InitWithContext(ctx,evaluation);if err!=nil{t.Fatal(err)}
 for _,n:=range []int64{1,2,3}{v,_:=sqltype.Integer(integer,n);if err=p.Advance(ctx,state,[]sqltype.Value{v,sqltype.Null(text)},evaluation);err!=nil{t.Fatal(err)}}
 ending,_:=state.(*transitionAggregateState).value.Integer();if ending!=123{t.Fatal("child-order support state changed",ending)}
 ordinary:=*p;aggregate:=*p.Aggregate;definition:=*p.Aggregate.Aggregate;definition.Definition.Kind="n";aggregate.Aggregate=&definition;ordinary.Aggregate=&aggregate
 if ownedOrderedAggregate(&ordinary){t.Fatal("ordinary aggregate lost executor-owned ordering")}
}

func TestAggregateOrderedIdentity(t *testing.T){
 p,_,_:=orderedUnitProvider(t)
 for _,change:=range []func(*catalog.OwnedAggregate){func(d *catalog.OwnedAggregate){d.Definition.Kind="h"},func(d *catalog.OwnedAggregate){d.Definition.DirectArgs++},func(d *catalog.OwnedAggregate){d.Variadic=true}}{
  other:=*p.Aggregate;definition:=*p.Aggregate.Aggregate;change(&definition);other.Aggregate=&definition
  if SameRoutineDeclaration(p.Aggregate,&other){t.Fatal("changed aggregate declaration retained identity")}
 }
}

func TestAggregateOrderedCancellation(t *testing.T){
 p,_,text:=orderedUnitProvider(t)
 ctx,cancel:=context.WithCancel(context.Background());defer cancel()
 evaluation:=&Context{EvalStatement:EvalStatement{Context:ctx}}
 state,err:=p.InitWithContext(ctx,evaluation);if err!=nil{t.Fatal(err)}
 before:=state.(*transitionAggregateState).value
 p.FinalCall=transitionUnitExpr{Info:expr.Info{Usage:text.Usage()},run:func(expr.Context)(sqltype.Value,error){cancel();return sqltype.String(text,"not published"),nil}}
 value,err:=p.FinalizeWithContext(ctx,state,[]sqltype.Value{sqltype.String(text,"p:")},evaluation)
 if !errors.Is(err,context.Canceled)||value.Type()!=nil||state.(*transitionAggregateState).value!=before{t.Fatal("canceled ordered final published or changed state",value,err)}
}
