package enginetests_test

import (
 "context"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/sqltype"
)

func TestLtreePathExpression(t *testing.T) {
 e:=engine.New();s:=ltreePathOwnershipSetup(t,e)
 ast:=enginetest.Statement(t,"SELECT lca(a,a,a,a,a,a,a,a) FROM lh_owner")
 op,err:=s.Acquire(context.Background());if err!=nil{t.Fatal(err)};defer op.Done()
 e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);if err!=nil{e.Mu.Unlock();t.Fatal(err)}
 binding,err:=analyze.Describe(op.Context(),e.AnalysisHost(op),ast,core.AuthorityOf(auth),ids.ObjectRef{},sqltype.InputEnvironment{},analyze.Expected{},nil,nil,false);e.Mu.Unlock();if err!=nil{t.Fatal(err)}
 project,ok:=binding.Query.Root.(*plan.ProjectNode);if !ok||len(project.Expressions)!=1{t.Fatal("missing projection")}
 call:=ltreePathOwnershipCall(t,project.Expressions[0]);saved:=call
 if call.Operation!=sqltype.LtreeLCA8||call.Count!=8{t.Fatal("bounded arity not preserved")}
 if admitted,set:=expr.SourceTraits(call);!admitted||set{t.Fatal("scalar source admission/cardinality")}
 if _,ok:=any(call).(expr.SourceOpening);!ok{t.Fatal("missing shared scalar source opener")}
 for _,kind:=range []expr.CaptureKind{expr.CaptureFunction,expr.CaptureOperator}{
  capture,ok,err:=call.Capture(kind);if err!=nil||!ok||capture.Count!=8||len(capture.Args)!=8{t.Fatal("repeated operands discarded",err)}
  for i,arg:=range capture.Args{if !reflect.DeepEqual(arg,call.Args[i]){t.Fatal("capture reordered occurrence",i)};if i>0&&arg.SourcePosition().Offset<=capture.Args[i-1].SourcePosition().Offset{t.Fatal("written order lost")}}
  capture.Args[0]=nil;if call.Args[0]==nil{t.Fatal("capture borrowed mutable argument buffer")}
 }
 children:=call.Children();next,err:=call.Rebuild(call.Info,children);if err!=nil||!reflect.DeepEqual(next,call){t.Fatal("rebuild changed exact source",err)};children[0]=nil
 if !reflect.DeepEqual(next,call)||!reflect.DeepEqual(call,saved){t.Fatal("rebuild retained mutable caller scratch")}
 spec,err:=call.Render();if err!=nil||len(spec.Args)!=8{t.Fatal("render lost arguments",err)}
 for i,arg:=range spec.Args{if arg.Transport!=expr.TransportCast||arg.TransportType.Type!=call.Formals[i]{t.Fatal("render lost exact formal",i)}}
 if !call.SameGroup(call,func(a,b expr.Expr)bool{return reflect.DeepEqual(a,b)},true){t.Fatal("same call not grouped")}
 bad:=call;bad.Count=7;if bad.Validate()==nil{t.Fatal("wrong active count admitted")}
 bad=call;bad.Formals[7]=nil;if bad.Validate()==nil{t.Fatal("last formal lost")}
 if _,err:=call.Rebuild(call.Info,call.Children()[:7]);err==nil{t.Fatal("rebuild discarded repeated operand")}
 identity:=func(d *sqltype.Descriptor)*sqltype.Descriptor{return d}
 if _,changed,err:=stored.RetagTypes(e.CatalogRead(),call,identity);err!=nil||changed{t.Fatal("identity retag",changed,err)}
 if !reflect.DeepEqual(call,saved){t.Fatal("rejected reconstruction changed original")}
}
