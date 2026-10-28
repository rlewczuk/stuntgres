package schemacmds_test

import (
	"errors"
 "fmt"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/sqltype"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/sqlstate"
)


func TestLtreeLtxtqueryProviderConflicts(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ltxtq_exec", "ltxtq_rexec", "forward_operator", "reverse_operator"} {
		t.Run("staged_"+name+"_conflict", func(t *testing.T) {
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
				return extension.StockLtree.Stage(&ltreeLtxtqueryConflictStage{Stage: host, name: name, owner: auth.State.CurrentUser}, ext, ext.Owner)
			}()
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
				t.Fatal("mixed conflict published a prefix", err)
			}
		})
	}
}

// Inject an incumbent only once both private scalar descriptors exist. The
// provider still performs its normal late name lookup and atomic stage abort.
type ltreeLtxtqueryConflictStage struct {
 extension.Stage
 name string
 owner authorization.PrincipalRef
 path *sqltype.Descriptor
}
func (s *ltreeLtxtqueryConflictStage) Create(d catalog.Descriptor) error {
 if err:=s.Stage.Create(d);err!=nil{return err}
 typ,ok:=d.(*catalog.Type);if !ok{return nil}
 if typ.SQLType.IsLtree(){s.path=typ.SQLType;return nil}
 if !typ.SQLType.IsLtxtquery(){return nil}
 left,right:=s.path,typ.SQLType
 if s.name=="ltxtq_rexec"||s.name=="reverse_operator"{left,right=right,left}
 oid,err:=s.AllocateOID();if err!=nil{return err};rev,err:=s.Revision();if err!=nil{return err}
 address:=ids.ObjectAddress{ClassID:ids.ProcedureClassID,ObjectID:oid}
 if s.name=="forward_operator"||s.name=="reverse_operator"{
  address.ClassID=ids.OperatorClassID
  return s.Stage.Create(&catalog.Operator{ObjectAddress:address,ObjectNamespace:typ.Namespace(),ObjectRevision:rev,ObjectName:"@",Owner:s.owner,Left:ids.OID(left.ID()),Right:ids.OID(right.ID()),Result:16})
 }
 return s.Stage.Create(&catalog.Procedure{ObjectAddress:address,ObjectNamespace:typ.Namespace(),ObjectRevision:rev,ObjectName:s.name,Owner:s.owner,Signature:catalog.ProcedureSignature(fmt.Sprintf("%d,%d",left.ID(),right.ID()))})
}
