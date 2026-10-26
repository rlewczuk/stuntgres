package schemacmds_test

import (
 "errors"
 "reflect"
 "testing"
 "stuntgres/internal/engine"
 "stuntgres/internal/engine/authorization"
 "stuntgres/internal/engine/catalog"
 "stuntgres/internal/engine/core"
 "stuntgres/internal/engine/enginetest"
 "stuntgres/internal/engine/extension"
 "stuntgres/internal/engine/ids"
 "stuntgres/internal/engine/schemacmds"
 "stuntgres/internal/parser"
 "stuntgres/internal/sqlstate"
 "stuntgres/internal/sqltype"
)

type ltxtqueryConflictStage struct {
	extension.Stage
	name  string
	owner authorization.PrincipalRef
}

func (s ltxtqueryConflictStage) Create(d catalog.Descriptor) error {
	if err := s.Stage.Create(d); err != nil {
		return err
	}
	typ, ok := d.(*catalog.Type)
	if !ok || !typ.SQLType.IsLtxtquery() {
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
	signature := extension.LquerySignature(typ.SQLType.ID())
	if s.name == "ltxtq_in" { signature = "2275" }
	return s.Stage.Create(&catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}, ObjectNamespace: typ.Namespace(), ObjectRevision: rev, ObjectName: s.name, Signature: signature, Owner: s.owner})
}

func ltxtqueryProviderConflicts(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ltxtq_in", "ltxtq_out"} {
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
				return extension.StockLtree.Stage(ltxtqueryConflictStage{Stage: host, name: name, owner: auth.State.CurrentUser}, ext, ext.Owner)
			}()
			e.Mu.Unlock()
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42723" || e.Catalog.State != before {
				t.Fatal("comparison conflict published a prefix", err)
			}
		})
	}
	t.Run("published_type_conflict", func(t *testing.T) {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		e.Mu.Lock()
		oid, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		arrayOID, err := e.Catalog.AllocateOIDLocked()
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		memberID, err := e.Catalog.AllocateEnumMemberLocked()
		if err != nil {
			e.Mu.Unlock()
			t.Fatal(err)
		}
		revision, err := e.Catalog.RevisionLocked()
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		typ, err := sqltype.EnumWithMembers(sqltype.ID(oid), "ltxtquery", "public.ltxtquery", []sqltype.EnumMember{{ID: memberID, Label: "occupied", SortOrder: 1}})
		if err != nil {
			t.Fatal(err)
		}
		array, err := sqltype.Array(sqltype.ID(arrayOID), "_ltxtquery", typ)
		if err != nil {
			t.Fatal(err)
		}
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		owner := op.RoleState().CurrentUser
		for _, d := range []*catalog.Type{
			{SQLType: typ, ObjectNamespace: namespace, Owner: owner, ObjectRevision: revision, Array: array.ID()},
			{SQLType: array, ObjectNamespace: namespace, Owner: owner, ObjectRevision: revision},
		} {
			if err := stage.Create(d); err != nil {
				t.Fatal(err)
			}
		}
		extensionUnitPublish(t, e, op, stage)
		before := e.Catalog.State
		e.Mu.Lock()
		_, err = schemacmds.StageExtensionLocked(e, &core.StatementScope{Op: op}, &parser.CreateExtension{Name: parser.Identifier{Text: "ltree"}})
		e.Mu.Unlock()
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42710", Message: `type "ltxtquery" already exists`, Context: "SQL statement \"CREATE TYPE ltxtquery (\n\tINTERNALLENGTH = -1,\n\tINPUT = ltxtq_in,\n\tOUTPUT = ltxtq_out,\n\tSTORAGE = extended\n)\"\nextension script file \"ltree--1.1.sql\", near line 424"}
		if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) || e.Catalog.State != before {
			t.Fatal("type conflict was not atomic/exact", diagnostic, err)
		}
	})
}
