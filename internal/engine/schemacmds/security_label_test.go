package schemacmds_test

import (
 "context"
 "errors"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
)

func TestSecurityLabelExecutorNoPublication(t *testing.T) {
 e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
 if _,err:=e.Execute(op,&parser.CreateRole{Name:parser.Identifier{Text:"label_reader"}},0,nil);err!=nil{t.Fatal(err)}
 for _, ordinary:=range []bool{false,true} {
  if ordinary {if _,err:=e.Execute(op,&parser.RoleSelection{Name:"label_reader"},0,nil);err!=nil{t.Fatal(err)}}
  state,roles,oid,revision,principal:=e.Catalog.State,e.RoleStore.Published,e.Catalog.NextOID(),e.Catalog.NextRevision(),e.RoleStore.NextPrincipal()
  identity,authRevision:=op.RoleState(),op.Session.Authorization.Revision
  unchanged:=func(){t.Helper();if e.Catalog.State!=state || e.RoleStore.Published!=roles || e.Catalog.NextOID()!=oid || e.Catalog.NextRevision()!=revision || e.RoleStore.NextPrincipal()!=principal || op.RoleState()!=identity || op.Session.Authorization.Revision!=authRevision {t.Fatal("security label changed authority")}}
  empty,mixed:="","MiSsInG"
  for _,provider:=range []*string{nil,&empty,&mixed} {
   stmt:=&parser.SecurityLabel{Provider:provider,Kind:"role",Name:parser.Identifier{Text:"missing"}}
   if _,err:=e.Describe(op,stmt);err!=nil{t.Fatal(err)};unchanged()
   result,err:=e.Execute(op,stmt,0,nil)
   message:="no security label providers have been loaded";if provider!=nil{message="security label provider \""+*provider+"\" is not loaded"}
   want:=sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:message};var got *sqlstate.Error
   if !errors.As(err,&got) || *got!=want || result.Rows!=nil || result.CommandTag!=""{t.Fatal(result,err,want)};unchanged()
  }
  old:=op.Ctx;ctx,cancel:=context.WithCancel(old);cancel();op.Ctx=ctx
  _,err:=e.Execute(op,&parser.SecurityLabel{Kind:"role"},0,nil);op.Ctx=old
  if !errors.Is(err,context.Canceled){t.Fatal(err)};unchanged()
 }
}
