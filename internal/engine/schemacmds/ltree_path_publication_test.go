package schemacmds_test

import (
	"errors"
 "fmt"
 "strings"
 "stuntgres/internal/engine/authorization"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/sqlstate"
)


type ltreePathConflictStage struct {
 extension.Stage
 name, signature string
 owner authorization.PrincipalRef
}
func (s ltreePathConflictStage) Create(d catalog.Descriptor) error {
 if err := s.Stage.Create(d); err != nil { return err }
 typ,ok := d.(*catalog.Type)
 if !ok || !typ.SQLType.IsArray() || !typ.SQLType.Element().IsLtree() { return nil }
 signature := strings.ReplaceAll(s.signature,"ltree[]",fmt.Sprint(typ.SQLType.ID()))
 signature = strings.ReplaceAll(signature,"ltree",fmt.Sprint(typ.SQLType.Element().ID()))
 oid,err := s.AllocateOID();if err != nil { return err }
 revision,err := s.Revision();if err != nil { return err }
 return s.Stage.Create(&catalog.Procedure{ObjectAddress:ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:oid},ObjectNamespace:typ.Namespace(),ObjectRevision:revision,ObjectName:s.name,Signature:catalog.ProcedureSignature(signature),Owner:s.owner})
}

func TestLtreePathProviderPublication(t *testing.T) {
	t.Helper()
 for _,tc := range []struct{name,signature string}{
  {"subltree","ltree,23,23"},{"subpath","ltree,23,23"},{"subpath","ltree,23"},
  {"index","ltree,ltree"},{"index","ltree,ltree,23"},{"nlevel","ltree"},
  {"ltree2text","ltree"},{"text2ltree","25"},{"lca","ltree[]"},
  {"lca","ltree,ltree"},{"lca","ltree,ltree,ltree"},{"lca","ltree,ltree,ltree,ltree"},
  {"lca","ltree,ltree,ltree,ltree,ltree"},{"lca","ltree,ltree,ltree,ltree,ltree,ltree"},
  {"lca","ltree,ltree,ltree,ltree,ltree,ltree,ltree"},{"lca","ltree,ltree,ltree,ltree,ltree,ltree,ltree,ltree"},
  {"ltree_addltree","ltree,ltree"},{"ltree_addtext","ltree,25"},{"ltree_textadd","25,ltree"},
 } {
  t.Run(tc.name+"_"+tc.signature, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			before := e.Catalog.State
			e.Mu.Lock()
			err := func() error {
				auth, err := e.AuthorizationLocked(op)
				if err != nil {
					return err
				}
				stage := e.BeginCatalogStage(auth.Catalog)
				defer e.DiscardCatalogStage(stage)
				ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
				stage.AuthorizeLocked(auth, op.Session)
				oid, err := e.Catalog.AllocateOIDLocked()
				if err != nil {
					return err
				}
				rev, err := e.Catalog.RevisionLocked()
				if err != nil {
					return err
				}
				ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectName: "ltree", ObjectRevision: rev, Version: "1.3", TargetNamespace: ns, Owner: auth.State.CurrentUser}
				if err := stage.Create(ext); err != nil {
					return err
				}
				host := &schemacmds.ExtensionHost{Engine: e, Auth: auth, Stage: stage}
    return extension.StockLtree.Stage(ltreePathConflictStage{Stage:host,name:tc.name,signature:tc.signature,owner:auth.State.CurrentUser},ext,ext.Owner)
			}()
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
    t.Fatal("path conflict published a prefix", err)
			}
		})
	}
 t.Run("invalid_late_manifest",func(t *testing.T) {
  e:=engine.New();op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
  stage:=lqueryProviderStage(t,e,op);candidate:=e.StageRead(stage)
  ext:=candidate.MustExtension(candidate.NameAddress(ids.NameKey{ClassID:ids.ExtensionClassID,Name:"ltree"}))
  candidate.MustProcedure(ext.Members[extension.LtreePathProcedureStart+18]).Display.Source="ltree_out"
  before:=e.CatalogRead().State()
  if _,err:=e.ValidateCatalogStage(t.Context(),stage);err==nil{t.Fatal("invalid late path declaration accepted")}
  if e.CatalogRead().State()!=before||e.CatalogRead().NameAddress(ids.NameKey{ClassID:ids.TypeClassID,Namespace:ext.TargetNamespace,Name:"ltree"})!=(ids.ObjectAddress{}){t.Fatal("invalid manifest published prefix")}
 })
}
