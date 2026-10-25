package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func extensionLiveAuthorityFences(t *testing.T) {
	for _, reason := range []string{"revoke", "inherit", "recreated_role"} {
		t.Run("actual_"+reason, func(t *testing.T) {
			e := engine.NewWithSettings(nil, "ext_fenced_db", false)
			admin, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			actor := roleAttributeCreate(t, e, admin, "ext_fenced_actor", true, parser.RoleOptions{})
			grantee := "ext_fenced_actor"
			if reason == "inherit" {
				roleAttributeCreate(t, e, admin, "ext_fenced_group", false, parser.RoleOptions{})
				grantee = "ext_fenced_group"
				roleAttributeApply(t, e, admin, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: grantee}}, Members: []parser.RoleSpec{{Name: actor.Name}}, Options: []parser.MembershipOption{{Name: "inherit", Value: true}}})
			}
			grant := extensionUnitGrant("ext_fenced_db", grantee)
			if reason == "recreated_role" {
				grant.Grantees = []parser.GrantGrantee{{Public: true}}
			}
			if err := extensionUnitExecute(t, e, admin, grant); err != nil {
				t.Fatal(err)
			}
			op, actorDone := enginetest.RoleOperation(t, e, actor.Name)
			defer actorDone()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(&parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}, 0, nil)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			switch reason {
			case "revoke":
				grant.Revoke = true
				if err := extensionUnitExecute(t, e, admin, grant); err != nil {
					t.Fatal(err)
				}
			case "inherit":
				roleAttributeApply(t, e, admin, &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: grantee}}, Members: []parser.RoleSpec{{Name: actor.Name}}, Options: []parser.MembershipOption{{Name: "inherit", Value: false}}})
			case "recreated_role":
				roleAttributeApply(t, e, admin, &parser.DropRole{Names: []string{actor.Name}})
				next := roleAttributeCreate(t, e, admin, actor.Name, true, parser.RoleOptions{})
				if next.Ref == actor.Ref {
					t.Fatal("role identity reused")
				}
			}
			before := e.Catalog.State
			if err := scope.Finish(nil); err == nil {
				t.Fatal("stale actual authority installed provider")
			}
			if e.Catalog.State != before || e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}) != (ids.ObjectAddress{}) {
				t.Fatal("authority fence partially published")
			}
			if reason != "recreated_role" {
				extensionUnitError(t, extensionUnitExecute(t, e, op, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}), "42501")
			}
		})
	}
	t.Run("savepoint_signature_keys", func(t *testing.T) {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		tx, err := e.Begin(op, session.TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		defer e.Rollback(op, tx)
		point := &parser.SavepointControl{Name: parser.Identifier{Text: "before_extension"}}
		if err := extensionUnitExecute(t, e, op, point); err != nil {
			t.Fatal(err)
		}
		install := &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}
		if err := extensionUnitExecute(t, e, op, install); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		private := e.PrivateCatalogLocked(session.TxOwnerOf(tx).Catalog)
		e.Mu.Unlock()
		a := private.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"})
		ext := private.MustExtension(a)
		for _, a := range ext.Members {
			d := private.Get(a)
			if private.NameAddress(catalog.DescriptorNameKey(d)) != a {
				t.Fatal("private signature identity lost")
			}
		}
		point.Kind = parser.RollbackSavepoint
		if err := extensionUnitExecute(t, e, op, point); err != nil {
			t.Fatal(err)
		}
		e.Mu.Lock()
		private = e.PrivateCatalogLocked(session.TxOwnerOf(tx).Catalog)
		e.Mu.Unlock()
		for _, a := range ext.Members {
			if private.Get(a) != nil {
				t.Fatal("savepoint member retained")
			}
		}
		if err := extensionUnitExecute(t, e, op, install); err != nil {
			t.Fatal("savepoint names not reusable", err)
		}
		if _, err := e.Commit(op, tx); err != nil {
			t.Fatal(err)
		}
		installed := e.Catalog.State.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
		if installed.Address() == ext.Address() {
			t.Fatal("rolled-back identity reused")
		}
		for _, a := range installed.Members {
			d := e.Catalog.State.Get(a)
			if e.Catalog.State.NameAddress(catalog.DescriptorNameKey(d)) != a {
				t.Fatal("committed signature identity lost")
			}
		}
	})
}

func extensionOwnershipImportUnits(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	// Actual isolated Restore stage supplies captured target-principal authority.
	stage := e.RestoreStage()
	op, done := enginetest.RoleOperation(t, stage, "postgres")
	defer done()
	if err := extensionUnitExecute(t, stage, op, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}); err != nil {
		t.Fatal(err)
	}
	encoded, err := extension.OwnerMetadata(e.CatalogRead(), e.Roles(), ext)
	if err != nil {
		t.Fatal(err)
	}
	record, _, err := extension.DecodeOwners(encoded[3 : len(encoded)-1])
	if err != nil {
		t.Fatal(err)
	}
	for i, slot := range []int{0, 1, 2, 3, 5} {
		if record.Members[i].Slot != slot || record.Members[i].Schema != "public" {
			t.Fatal("owner metadata lost installed slot/schema", record.Members[i])
		}
	}
	before := stage.Catalog.State
	for _, kind := range []string{"namespace", "version", "member_owner", "duplicate", "member_identity", "member_name", "member_namespace", "member_slot", "duplicate_slot", "missing_member"} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		records := []extension.OwnerRecord{bad}
		switch kind {
		case "namespace":
			records[0].Schema = "absent"
		case "version":
			records[0].Version = "missing"
		case "member_owner":
			records[0].Members[0].Owner = "absent_owner"
		case "duplicate":
			records = append(records, bad)
		case "member_identity":
			records[0].Members[0].Signature = "invalid"
		case "member_name":
			records[0].Members[0].Name = "unreplayed_label"
		case "member_namespace":
			records[0].Members[0].Schema = "pg_catalog"
		case "member_slot":
			records[0].Members[0].Slot = len(ext.Members)
		case "duplicate_slot":
			records[0].Members[1].Slot = records[0].Members[0].Slot
		case "missing_member":
			records[0].Members = records[0].Members[1:]
		}
		if err := schemacmds.ImportExtensionOwners(stage, context.Background(), op, records); err == nil {
			t.Fatal("invalid ownership import accepted", kind)
		}
		if stage.Catalog.State != before {
			t.Fatal("failed ownership import changed staged identities", kind)
		}
	}
	if err := schemacmds.ImportExtensionOwners(stage, context.Background(), op, []extension.OwnerRecord{record}); err != nil {
		t.Fatal(err)
	}
	// Central DROP dependencies include extension and every owned member, plus
	// the destination database's real grantor/grantee references.
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	var dependencies []access.RoleDependency
	access.DescriptorRoleDependencies(ext, owner, &dependencies)
	if len(dependencies) != 1 || dependencies[0].Address != ext.Address() || dependencies[0].Kind != 'o' || !access.DescriptorRoleDependencies(ext, owner, nil) {
		t.Fatal("extension owner dependency", dependencies)
	}
	for _, a := range ext.Members {
		d := e.Catalog.State.Get(a)
		refs := descriptorReferences(d)
		if a.ClassID == ids.TSTemplateClassID {
			if len(refs) != 0 {
				t.Fatal("template owner dependency")
			}
		} else {
			found := false
			for _, ref := range refs {
				found = found || ref == owner
			}
			if !found {
				t.Fatal("member owner dependency", a)
			}
		}
	}
	target := engine.New()
	admin, adminDone := enginetest.RoleOperation(t, target, "postgres")
	defer adminDone()
	originalOwner := roleAttributeCreate(t, target, admin, "ext_import_owner", true, parser.RoleOptions{})
	imported := target.RestoreStage()
	replay, replayDone := enginetest.RoleOperation(t, imported, "postgres")
	defer replayDone()
	if err := extensionUnitExecute(t, imported, replay, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}); err != nil {
		t.Fatal(err)
	}
	ownerRecord := record
	ownerRecord.Members = append([]extension.MemberOwner(nil), record.Members...)
	for i := range ownerRecord.Members {
		ownerRecord.Members[i].Owner = originalOwner.Name
	}
	if err := schemacmds.ImportExtensionOwners(imported, context.Background(), replay, []extension.OwnerRecord{ownerRecord}); err != nil {
		t.Fatal(err)
	}
	roleAttributeApply(t, target, admin, &parser.DropRole{Names: []string{originalOwner.Name}})
	replacementOwner := roleAttributeCreate(t, target, admin, originalOwner.Name, true, parser.RoleOptions{})
	if replacementOwner.Ref == originalOwner.Ref {
		t.Fatal("target role identity reused")
	}
	incumbent := target.Catalog.State
	if _, err := target.ReplaceCatalogInOperation(context.Background(), imported, imported.CatalogRead(), admin); err == nil {
		t.Fatal("member owner target recreation escaped import fence")
	}
	if target.Catalog.State != incumbent {
		t.Fatal("member owner fence replaced target")
	}
}

func extensionUnitGrant(name string, role string) *parser.ObjectGrant {
	return &parser.ObjectGrant{Kind: "database", Privileges: []parser.GrantPrivilege{{Name: "create"}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: name}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: role}}}}
}

func extensionUnitError(t *testing.T, err error, code string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("got %v, want SQLSTATE %s", err, code)
	}
}
