package schemacmds_test

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/sqlstate"
)


type ltreeLqueryConflictStage struct {
	extension.Stage
	name string
	owner authorization.PrincipalRef
	query ids.OID
}

func (s *ltreeLqueryConflictStage) Create(d catalog.Descriptor) error {
	if err := s.Stage.Create(d); err != nil { return err }
	typ, ok := d.(*catalog.Type)
	if !ok { return nil }
	if typ.SQLType.IsLquery() { s.query = ids.OID(typ.SQLType.ID()); return nil }
	if !typ.SQLType.IsLtree() { return nil }
	left, right := ids.OID(typ.SQLType.ID()), s.query
	if s.name == "ltq_rregex" { left, right = right, left }
	oid, err := s.AllocateOID(); if err != nil { return err }
	revision, err := s.Revision(); if err != nil { return err }
	return s.Stage.Create(&catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: typ.Namespace(), ObjectRevision: revision, ObjectName: s.name, Signature: catalog.ProcedureSignature(fmt.Sprintf("%d,%d", left, right)), Owner: s.owner})
}

func TestLtreeLqueryProviderConflicts(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ltq_regex", "ltq_rregex"} {
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
				return extension.StockLtree.Stage(&ltreeLqueryConflictStage{Stage: host, name: name, owner: auth.State.CurrentUser}, ext, ext.Owner)
			}()
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
				t.Fatal("lquery conflict published a prefix", err)
			}
		})
	}
}
