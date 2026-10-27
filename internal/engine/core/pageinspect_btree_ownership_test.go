package core_test

import (
 "errors"
 "testing"
 rootengine "stuntgres/internal/engine"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/ids"
)

// Public SQL results live in reference/extensions. This exercises actual
// registry/portal graph ownership and rejected retained-state publication.
func TestPageinspectBtreePreparedOwnership(t *testing.T){
 e:=rootengine.New();s:=enginetest.StartSession(t,e,nil)
 enginetest.SessionSQL(t,s,"CREATE EXTENSION pageinspect");enginetest.SessionSQL(t,s,"BEGIN")
 cycle:=s.NewProtocolCycle();command:=func(fn func(*core.StatementScope)error){preparedFoldProtocolCommand(t,e,s,cycle,fn)}
 parse:=func(){command(func(scope *core.StatementScope)error{_,err:=scope.ParseProtocol("bt_owned","SELECT * FROM bt_page_items(NULL::bytea)",nil);return err})}
 bind:=func(name string){command(func(scope *core.StatementScope)error{_,err:=scope.BindProtocolPortal(name,"bt_owned",&protocolUnitInput{data:[]byte{0,0,0,0,0,0}});return err})}
 parse();registry:=core.PayloadOf(s).PreparedSQL;definition:=registry.Lookup("bt_owned");owner:=definition.Description.Folds
 if owner==nil || !owner.Live(){t.Fatal("missing actual prepared owner")}
 bind("bt_old");portal:=registry.Binding("bt_old");root:=portal.Resource.Root;accepted:=owner.Current();analysis:=definition.Description.Analysis
 if portal.Entry.Description.Folds!=owner || root==nil || analysis==nil || analysis.Domain!=e.Catalog.Domain || analysis.Epoch!=e.CatalogRead().Epoch(){t.Fatal("lost definition/domain/graph authority")}
 found:=false;for _,dep:=range analysis.Dependencies{if dep.Ref.Address.ClassID==ids.ProcedureClassID{p,ok:=e.CatalogRead().Procedure(dep.Ref.Address);if ok && p.Display.Source=="bt_page_items_bytea"{found=true;if dep.Ref.Domain()!=analysis.Domain || dep.Ref.Epoch!=analysis.Epoch || dep.Revision!=p.Revision(){t.Fatal("lost provider dependency",dep)}}}}
 if !found{t.Fatal("missing real provider dependency")}
 bind("bt_again");if registry.Binding("bt_again").Resource.Root==root || registry.Binding("bt_again").Entry.Description.Folds!=owner || owner.Current().Generation!=accepted.Generation{t.Fatal("graphs or definition generations conflated")}
 // Capture a genuine next publication, then retire its exact definition by
 // closing the statement. The retained portal remains independent and usable.
 var publication *core.FoldPublication;var environment core.PreparedEnvironment
 op,err:=s.Acquire(t.Context());if err!=nil{t.Fatal(err)}
 e.Mu.Lock();auth,err:=e.AuthorizationLocked(op);if err==nil{environment,err=e.PreparedEnvironmentLocked(auth,true)};if err==nil{publication,err=core.BeginFoldPublication(&definition.Description,definition.Body,environment,auth.Catalog,nil)};e.Mu.Unlock();op.Done();if err!=nil || publication==nil{t.Fatal(publication,err)}
 command(func(scope *core.StatementScope)error{return scope.CloseProtocolStatement("bt_owned")})
 if owner.Live() || portal.Retired || portal.Resource.Root!=root || portal.Entry.Description.Analysis!=analysis{t.Fatal("statement retirement mutated retained portal")}
 op,err=s.Acquire(t.Context());if err!=nil{t.Fatal(err)};e.Mu.Lock();auth,err=e.AuthorizationLocked(op);if err==nil{environment,err=e.PreparedEnvironmentLocked(auth,true)};if err==nil{err=publication.Accept(op.Ctx,auth.Catalog,environment,e.Catalog.State.Epoch())};e.Mu.Unlock();op.Done()
 if !errors.Is(err,ids.ErrCatalogReplaced) || publication.Group.Sealed || owner.Current().Generation<accepted.Generation || portal.Resource.Root!=root{t.Fatal("retired definition published or damaged old graph",err)}
 parse();if registry.Lookup("bt_owned").Description.Folds==owner{t.Fatal("new definition adopted retired owner")};bind("bt_new")
 if portal.Resource.Root!=root || portal.Entry.Description.Analysis!=analysis{t.Fatal("replacement mutated old immutable analysis")}
 command(func(scope *core.StatementScope)error{return scope.CloseProtocolPortal("bt_old")})
}
