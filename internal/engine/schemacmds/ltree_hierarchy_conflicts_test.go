package schemacmds_test

import (
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/sqlstate"
)


func TestLtreeHierarchyProviderConflicts(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ltree_isparent", "ltree_risparent"} {
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
				return extension.StockLtree.Stage(ltreeConflictStage{Stage: host, name: name, owner: auth.State.CurrentUser}, ext, ext.Owner)
			}()
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
				t.Fatal("hierarchy conflict published a prefix", err)
			}
		})
	}
}
