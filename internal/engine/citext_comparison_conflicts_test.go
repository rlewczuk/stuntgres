package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

type citextConflictStage struct {
	extension.Stage
	name  string
	owner authorization.PrincipalRef
}

func (s citextConflictStage) Create(d catalog.Descriptor) error {
	if err := s.Stage.Create(d); err != nil {
		return err
	}
	typ, ok := d.(*catalog.Type)
	if !ok || !typ.SQLType.IsCitext() {
		return nil
	}
	oid, err := s.AllocateOID()
	if err != nil {
		return err
	}
	rev, err := s.Revision()
	if err != nil {
		return err
	}
	return s.Stage.Create(&catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: typ.Namespace(), ObjectRevision: rev, ObjectName: s.name, Signature: extension.CitextComparisonSignature(typ.SQLType.ID()), Owner: s.owner})
}

func TestCitextComparisonProviderConflicts(t *testing.T) {
	t.Helper()
	for _, name := range []string{"citext_eq", "citext_ge"} {
		t.Run("staged_"+name+"_conflict", func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
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
				stage.ExtensionNamespace = ns
				oid, err := e.Catalog.AllocateOIDLocked()
				if err != nil {
					return err
				}
				rev, err := e.Catalog.RevisionLocked()
				if err != nil {
					return err
				}
				ext := &catalog.Extension{ObjectAddress: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: oid}, ObjectName: "citext", ObjectRevision: rev, Version: "1.8", TargetNamespace: ns, Owner: auth.State.CurrentUser}
				if err := stage.Create(ext); err != nil {
					return err
				}
				host := &extensionHost{engine: e, auth: auth, stage: stage}
				return extension.StockCitext.Stage(citextConflictStage{Stage: host, name: name, owner: auth.State.CurrentUser}, ext, ext.Owner)
			}()
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
				t.Fatal("comparison conflict published a prefix", err)
			}
		})
	}
}
