package enginetests_test

import (
 "context"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/analyze"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/stored"
 "stuntgres/internal/sqltype"
)

func TestHstoreComparisonOwnership(t *testing.T){
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,"CREATE EXTENSION hstore")
 enginetest.SessionSQL(t,s,"CREATE TABLE hs_comparison_owner(v hstore)")
 ast:=enginetest.Statement(t,"SELECT hstore_eq(v,v),hstore_ne(v,v),hstore_lt(v,v),hstore_le(v,v),hstore_gt(v,v),hstore_ge(v,v),hstore_cmp(v,v),hstore_hash(v),hstore_hash_extended(v,1::bigint) FROM hs_comparison_owner")
 op,err:=s.Acquire(context.Background());if err!=nil{t.Fatal(err)};defer op.Done()
 e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);if err!=nil{e.Mu.Unlock();t.Fatal(err)}
 binding,err:=analyze.Describe(op.Context(),e.AnalysisHost(op),ast,core.AuthorityOf(auth),ids.ObjectRef{},sqltype.InputEnvironment{},analyze.Expected{},nil,nil,false);e.Mu.Unlock();if err!=nil{t.Fatal(err)}
 project,ok:=binding.Query.Root.(*plan.ProjectNode);if !ok||len(project.Expressions)!=9{t.Fatal("missing selected projection")}
 view:=e.CatalogRead();identity:=func(d *sqltype.Descriptor)*sqltype.Descriptor{return d}
 installation:=view.MustExtension(view.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"hstore"}))
 for i,node:=range project.Expressions{t.Run(extension.HstoreComparisonNames[i],func(t *testing.T){
  call,ok:=node.(plan.HstoreComparisonExpr);if !ok{t.Fatalf("missing retained provider %T",node)}
  selected:=view.MustProcedure(installation.Members[extension.HstoreComparisonProcedureStart+i])
  if call.RoutineReference!=selected||call.Kind!=uint8(i)||call.Occurrence==0||call.Ref.Domain()!=view.Owner().Domain||call.Ref.Epoch!=view.Epoch(){t.Fatal("lost exact selected capture")}
  originalArgs:=append([]expr.Expr(nil),call.Args...)
  rebuilt,err:=call.Rebuild(call.Info,call.Children());if err!=nil{t.Fatal(err)}
  next:=rebuilt.(plan.HstoreComparisonExpr)
  if next.Info!=call.Info||next.Ref!=call.Ref||!reflect.DeepEqual(next.Args,call.Args)||!call.SameGroup(next,func(a,b expr.Expr)bool{return reflect.DeepEqual(a,b)},false){t.Fatal("rebuild changed provenance")}
  next.Args[0]=nil
  if !reflect.DeepEqual(call.Args,originalArgs){t.Fatal("rebuild borrowed child slice")}
  operands,ok,err:=call.Capture(expr.CaptureFunction);if err!=nil||!ok||operands.Count!=len(call.Args){t.Fatal("capture shape",err)}
  for j,a:=range call.Args{if !reflect.DeepEqual(operands.Inline[j],a){t.Fatal("capture order")}}
  if _,changed,err:=stored.RetagTypes(view,node,identity);err!=nil||changed{t.Fatal("identity retag changed source",err)}
  if remapped,err:=stored.RemapExpression(node,func(r ids.ObjectRef)ids.ObjectRef{return r},nil);err!=nil||!reflect.DeepEqual(remapped,node){t.Fatal("identity reference remap",err)}
  collector:=stored.DependRefs{Context:context.Background(),Catalog:view};collector.Expression(node)
  if _,found:=collector.Addresses[selected.Address()];collector.Err!=nil||!found{t.Fatal("selected dependency missing",collector.Err)}
  rendering,err:=call.Render();if err!=nil||len(rendering.Args)!=len(call.Args){t.Fatal("render operands",err)}
  for j,a:=range rendering.Args{if a.Transport!=expr.TransportCast||a.TransportType!=call.Args[j].ResultUsage(){t.Fatal("lost exact formal replay")}}
  if p,err:=exec.ExpressionRoutineDescriptor(node);err!=nil||p!=selected{t.Fatal("lost immutable declaration",err)}
  t.Run("historical_declaration",func(t *testing.T){
   if selected.Revision()<=1{t.Fatal("fixture requires an earlier nonzero revision")}
   historical:=*selected;historical.ObjectRevision=selected.Revision()-1
   retained:=call;retained.RoutineReference=&historical
   next,changed,err:=stored.RetagTypes(view,retained,identity)
   if err!=nil||changed{t.Fatal("historical declaration rejected or rewritten",err)}
   if next.(plan.HstoreComparisonExpr).RoutineReference!=&historical||call.RoutineReference!=selected{t.Fatal("historical or source backing replaced")}
  })
  mutationNames:=[]string{"wrong_role","absent_reference","future_epoch","foreign_domain","wrong_symbol","future_revision","foreign_provider","foreign_result"}
  for mutationIndex,mutate:=range []func(*plan.HstoreComparisonExpr){
   func(n *plan.HstoreComparisonExpr){n.Kind=uint8((i+1)%9)},
   func(n *plan.HstoreComparisonExpr){n.Ref=ids.ObjectRef{}},
   func(n *plan.HstoreComparisonExpr){n.Ref.Epoch++},
   func(n *plan.HstoreComparisonExpr){foreign:=engine.New().CatalogRead();n.Ref=foreign.Owner().Domain.Ref(view.Epoch(),selected.Address())},
   func(n *plan.HstoreComparisonExpr){p:=*selected;p.Display.Source="hstore_out";n.RoutineReference=&p},
   func(n *plan.HstoreComparisonExpr){p:=*selected;p.ObjectRevision=selected.Revision()+1;n.RoutineReference=&p},
   func(n *plan.HstoreComparisonExpr){p:=*selected;p.Implementation=extension.StockCube;n.RoutineReference=&p},
   func(n *plan.HstoreComparisonExpr){foreign:=*n.Usage.Type;n.Usage.Type=&foreign},
  }{t.Run(mutationNames[mutationIndex],func(t *testing.T){bad:=call;mutate(&bad);if _,_,err:=stored.RetagTypes(view,bad,identity);err==nil{t.Fatal("invalid routine/generation capture accepted")};if call.RoutineReference!=selected||!reflect.DeepEqual(call.Args,originalArgs){t.Fatal("rejection mutated source")}})}
  for _,mutate:=range []func(*catalog.Procedure){
   func(p *catalog.Procedure){p.Result=25},
   func(p *catalog.Procedure){p.Signature="25"},
   func(p *catalog.Procedure){p.Strict=false},
   func(p *catalog.Procedure){p.Implementation=extension.StockCube},
  }{candidate:=catalog.CloneState(view.State());p:=*selected;mutate(&p);candidate.SetDescriptor(p.Address(),&p);if _,_,err:=stored.RetagTypes(view.WithState(candidate),call,identity);err==nil{t.Fatal("invalid current authority accepted")}}
 })}
}
