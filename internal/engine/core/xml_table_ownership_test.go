package core_test

import (
 "context"
 "errors"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/expr"
 "stuntgres/internal/engine/functions"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
 "testing"
)

func xmlTableOwnedProvider(t *testing.T,root *plan.ProjectNode) functions.XMLTable {
 t.Helper();var result functions.XMLTable;found:=false
 plan.VisitExecutableQuery(root,func(plan.Node){},func(node expr.Expr){plan.WalkExpression(node,func(node expr.Expr){if table,ok:=node.(functions.XMLTable);ok {result=table;found=true}})})
 if !found {t.Fatal("accepted graph lost XMLTABLE provider")};return result
}
func xmlTableOwnedDocument(t *testing.T,root *plan.ProjectNode) sqltype.Value {
 t.Helper()
 provider:=xmlTableOwnedProvider(t,root)
 var result sqltype.Value
 plan.WalkExpression(provider.Args[len(provider.Raw.Namespaces)+1],func(node expr.Expr){
  if literal,ok:=node.(plan.LiteralExpr);ok && literal.Value.Type()!=nil && literal.Value.Type().ID()==sqltype.XML {result=literal.Value}
 })
 if result.Type()==nil {t.Fatal("accepted XMLTABLE document lost its XML input artifact")}
 return result
}
func TestXMLTableProviderOwnership(t *testing.T) {
 e:=engine.New();s:=enginetest.StartSession(t,e,nil)
 transaction:=enginetest.TransactionBegin(t,s);defer enginetest.TransactionEnd(t,s,transaction,false)
 cycle:=s.NewProtocolCycle()
 command:=func(fn func(*core.StatementScope)error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
 command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("xt_owned",`SELECT length('owned') AS folded,x.v FROM XMLTABLE('/r' PASSING XML '<r/>' COLUMNS v xml PATH '"scalar"') x`,nil);return err})
 registry:=core.PayloadOf(s).PreparedSQL
 definition:=registry.Lookup("xt_owned")
 if definition==nil || definition.Description.Analysis==nil || definition.Description.Folds==nil {t.Fatal("missing actual accepted description")}
 owner:=definition.Description.Folds
 if owner.Current().Generation!=0 {t.Fatal("parse published execution folds")}
 bind:=func(name string)*core.PreparedBinding {
  command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal(name,"xt_owned",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}});return err})
  binding:=registry.Binding(name)
  if binding==nil || binding.Resource==nil || binding.Resource.Root==nil || binding.Folds==nil {t.Fatal("missing actual binding")}
  return binding
 }
 first:=bind("xt_first");second:=bind("xt_second")
 accepted:=owner.Current()
 if len(accepted.Entries)==0 || first.Folds.Generation!=accepted.Generation || second.Folds.Generation!=accepted.Generation || &first.Folds.Entries[0]!=&accepted.Entries[0] || &second.Folds.Entries[0]!=&accepted.Entries[0] {t.Fatal("bindings did not retain actual accepted generation/backing")}
 graph:=first.Resource.Root;other:=second.Resource.Root
 provider:=xmlTableOwnedProvider(t,graph);sibling:=xmlTableOwnedProvider(t,other)
 if provider.ValidateStoredProvider()!=nil || sibling.ValidateStoredProvider()!=nil || provider.Usage.Type.CompositeMember(0).Usage.Type.ID()!=sqltype.XML {t.Fatal("accepted member authority")}
 args,fields,raw:=&provider.Args[0],&provider.Fields[0],provider.Raw
 document:=xmlTableOwnedDocument(t,graph)
 if !sqltype.SameDatum(document,xmlTableOwnedDocument(t,other)) {t.Fatal("binding copied accepted XML artifact")}
 func(){
  op,err:=s.Acquire(context.Background());if err!=nil {t.Fatal(err)};defer op.Done()
  e.Mu.Lock();defer e.Mu.Unlock()
  auth,err:=e.AuthorizationLocked(op);if err!=nil {t.Fatal(err)}
  env,err:=e.PreparedEnvironmentLocked(auth,true);if err!=nil {t.Fatal(err)}
  publication,err:=core.BeginFoldPublication(&definition.Description,definition.Body,env,auth.Catalog,nil);if err!=nil {t.Fatal(err)}
  canceled,cancel:=context.WithCancel(context.Background());cancel()
  err=publication.Accept(canceled,auth.Catalog,env,e.Catalog.State.Epoch())
  current:=owner.Current()
  if !errors.Is(err,context.Canceled) || publication.Group.Sealed || current.Generation!=accepted.Generation || &current.Entries[0]!=&accepted.Entries[0] || &xmlTableOwnedProvider(t,graph).Args[0]!=args {t.Fatal("rejected publication changed accepted provider generation/backing",err)}
 }()
 xmlPreparedMode(t,e,s,"document")
 op,_,err:=s.AcquireCommand(context.Background());if err!=nil {t.Fatal(err)}
 scope,err:=e.StartProtocolCommand(op,cycle);if err!=nil {op.Done();t.Fatal(err)}
 _,err=scope.ExecuteProtocolPortal("xt_first",0,func([]session.ProtocolField)error{return nil},nil,nil,core.CopyProtocolHooks{})
 failure:=scope.Finish(err);op.Done()
 var diagnostic *sqlstate.Error
 if !errors.As(failure,&diagnostic) || diagnostic.Code!="2200M" {t.Fatal("missing genuine later execution failure",failure)}
 retained:=xmlTableOwnedProvider(t,graph)
 current:=owner.Current()
 if retained.Raw!=raw || &retained.Args[0]!=args || &retained.Fields[0]!=fields || current.Generation!=accepted.Generation || &current.Entries[0]!=&accepted.Entries[0] || !sqltype.SameDatum(document,xmlTableOwnedDocument(t,graph)) {t.Fatal("failed producer mutated accepted backing")}
 if first.Resource.Root!=nil {t.Fatal("failed producer retained live graph")}
 if second.Resource.Root!=other || xmlTableOwnedProvider(t,other).Raw!=sibling.Raw {t.Fatal("failed invocation detached sibling graph")}
}
