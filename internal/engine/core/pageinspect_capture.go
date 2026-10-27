package core

import (
 "fmt"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/exec"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/index"
 "stuntgres/internal/engine/lock"
 "stuntgres/internal/engine/plan"
 "stuntgres/internal/engine/session"
 "stuntgres/internal/sqltype"
)

// Pageinspect captures only immutable catalog descriptors. No physical state or
// table rows are consulted. Engine.Mu protects admission, never byte decoding.
func(h *statementHost)Pageinspect(n plan.InstalledPageinspectExpr,oid uint32)([]sqltype.BrinStoredColumn,error){
 s,e:=h.scope(),h.scope().Engine();if err:=s.Active();err!=nil{return nil,err}
 e.Mu.Lock();auth,err:=s.RoutineAuthorizationLocked()
 if err==nil {err=pageinspectAuthority(auth,n)}
 var target *catalog.Relation
 var ref ids.ObjectRef
 if err==nil && n.Member=="brin_page_items" {
  ref=e.Catalog.Domain.Ref(auth.Catalog.Epoch(),ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:ids.OID(oid)})
  target,_=auth.Catalog.Relation(ref.Address)
  if target==nil {err=SQLError("XX000",fmt.Sprintf("could not open relation with OID %d",oid))}
 }
 e.Mu.Unlock();if err!=nil{return nil,err};if target==nil{return nil,nil}
 var contribution lock.RelationContribution;defer e.Locks.ReleaseRelation(&contribution)
 policy:=lock.Policy{TimeoutSlot:&s.Op.LockWait,Timeout:s.Op.LockTimeout,StatementDeadline:s.Op.StatementDeadline,Source:s.Op.SourceContext}
 request,err:=e.Locks.StartAcquireRelation(s.Op.Context(),s.LockTicket,lock.Resource{ObjectRef:ref},lock.AccessShare,policy,&contribution)
 if request!=nil {s.admissionWaited=true;if s.ReplacementRead{e.ReplacementMu.RUnlock();s.ReplacementRead=false};err=request.Wait()}
 if err!=nil{return nil,err}
 s.Owner.Mu.Lock();implicit:=s.Owner.State==session.TxImplicit;s.Owner.Mu.Unlock()
 if implicit && !s.ReplacementRead {e.ReplacementMu.RLock();s.ReplacementRead=true}
 s.Owner.Mu.Lock();e.Mu.Lock()
 defer s.Owner.Mu.Unlock();defer e.Mu.Unlock()
 auth,err=s.RoutineAuthorizationLocked();if err!=nil{return nil,err};if err=pageinspectAuthority(auth,n);err!=nil{return nil,err}
 if ref.Epoch!=auth.Catalog.Epoch() || auth.Catalog.Get(ref.Address)!=target{return nil,ids.ErrCatalogReplaced}
 e.markTemporaryAccessLocked(s,ref,temporaryTargetAttempt)
 if target.RelationKind!=catalog.IndexRelation || target.SQLIndex==nil{return nil,SQLError("42809",fmt.Sprintf("\"%s\" is not an index",target.Name()))}
 if target.SQLIndex.Method!=catalog.BrinMethod{return nil,SQLError("42809",fmt.Sprintf("\"%s\" is not a BRIN index",target.Name()))}
 parent,ok:=auth.Catalog.Relation(target.SQLIndex.Parent);if !ok{return nil,ids.ErrCatalogReplaced}
 bindings,err:=index.BindBrinIndex(auth.Catalog,parent,target.SQLIndex);if err!=nil{return nil,err}
 columns:=make([]sqltype.BrinStoredColumn,len(bindings))
 for i,b:=range bindings {
  storage,ok:=auth.Catalog.LookupType(sqltype.ID(b.Class.StorageOID()))
  if !ok{return nil,fmt.Errorf("stuntgres: missing BRIN stored type")}
  columns[i],err=sqltype.BindBrinStoredColumn(b.Usage,b.Class,storage);if err!=nil{return nil,err}
 }
 return columns,nil
}
func pageinspectAuthority(auth AuthorizationView,n plan.InstalledPageinspectExpr)error{
 current,ok:=auth.Catalog.Procedure(n.Ref.Address)
 if !ok || n.RoutineReference!=n.Procedure || !exec.SameRoutineDeclaration(current,n.Procedure){return ids.ErrCatalogReplaced}
 if err:=extension.ValidatePageinspectIdentity(auth.Catalog,current,n.Ref);err!=nil{return err}
 symbol,err:=extension.ProcedureSymbol(auth.Catalog,current);if err!=nil{return err};if symbol!=n.Member{return fmt.Errorf("stuntgres: invalid pageinspect call member")}
 actor,err:=auth.Roles.Descriptor(auth.State.CurrentUser);if err!=nil{return err}
 if !actor.Superuser{return SQLError("42501","must be superuser to use raw page functions")};return nil
}
