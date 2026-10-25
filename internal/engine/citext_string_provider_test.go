package engine

import (
 "errors"
 "testing"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/sqlstate"
)

// Inject a real private-stage descriptor at the last string signature lookup.
// Discarding the failed stage must publish none of the preceding members.
type citextStringCollisionStage struct {
 *extensionHost
 injected bool
 err error
}
func (h *citextStringCollisionStage) LookupName(key ids.NameKey) ids.ObjectAddress {
 if !h.injected&&key.ClassID==ids.ProcedureClassID&&key.Name=="translate" {
  h.injected=true
  oid,err:=h.AllocateOID();if err!=nil {h.err=err;return ids.ObjectAddress{}}
  revision,err:=h.Revision();if err!=nil {h.err=err;return ids.ObjectAddress{}}
  h.err=h.Create(&catalog.Procedure{ObjectAddress:ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:oid},ObjectNamespace:key.Namespace,ObjectRevision:revision,ObjectName:key.Name,Signature:catalog.ProcedureSignature(key.Signature),Owner:h.Auth.State.CurrentUser})
 }
 return h.extensionHost.LookupName(key)
}
func citextStringLateConflict(t *testing.T) {
 t.Helper();e:=New();op,done:=roleOperation(t,e,"postgres");defer done();before:=e.Catalog.State
 e.Mu.Lock()
 var injected bool
 err:=func()error{
  auth,err:=e.AuthorizationLocked(op);if err!=nil {return err}
  stage:=e.BeginCatalogStage(auth.Catalog);defer e.DiscardCatalogStage(stage)
  stage.AuthorizeLocked(auth,op.Session)
  namespace:=ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID}
  oid,err:=e.Catalog.AllocateOIDLocked();if err!=nil {return err}
  revision,err:=e.Catalog.RevisionLocked();if err!=nil {return err}
  installation:=&catalog.Extension{ObjectAddress:ids.ObjectAddress{ClassID:ids.ExtensionClassID,ObjectID:oid},ObjectRevision:revision,ObjectName:"citext",Version:"1.8",TargetNamespace:namespace,Owner:auth.State.CurrentUser}
  if err:=stage.Create(installation);err!=nil {return err}
  host:=&citextStringCollisionStage{extensionHost:&extensionHost{Engine:e,Auth:auth,Stage:stage}}
  err=extension.StockCitext.Stage(host,installation,installation.Owner);injected=host.injected
  if host.err!=nil {return host.err};return err
 }()
 e.Mu.Unlock()
 var diagnostic *sqlstate.Error
 if !injected||!errors.As(err,&diagnostic)||diagnostic.Code!="42723"||diagnostic.Message!=`function "translate" already exists with same argument types`||e.Catalog.State!=before {t.Fatal("late string collision published an installation prefix",injected,err)}
}
