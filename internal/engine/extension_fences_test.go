package engine

import (
	"context"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"testing"
)

func extensionLiveAuthorityFences(t *testing.T) {
	for _, reason := range []string{"revoke", "inherit", "recreated_role"} {
		t.Run("actual_"+reason, func(t *testing.T) {
			e := NewWithSettings(nil, "ext_fenced_db", false)
			admin, done := roleOperation(t, e, "postgres")
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
			op, actorDone := roleOperation(t, e, actor.Name)
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
			before := e.catalog.state
			if err := scope.Finish(nil); err == nil {
				t.Fatal("stale actual authority installed provider")
			}
			if e.catalog.state != before || e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}) != (ids.ObjectAddress{}) {
				t.Fatal("authority fence partially published")
			}
			if reason != "recreated_role" {
				extensionUnitError(t, extensionUnitExecute(t, e, op, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}), "42501")
			}
		})
	}
	t.Run("savepoint_signature_keys", func(t *testing.T) {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		tx, err := e.Begin(op, TxOptions{})
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
		e.mu.Lock()
		private := e.privateCatalogLocked(txOwnerOf(tx).Catalog)
		e.mu.Unlock()
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
		e.mu.Lock()
		private = e.privateCatalogLocked(txOwnerOf(tx).Catalog)
		e.mu.Unlock()
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
		installed := e.catalog.state.MustExtension(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
		if installed.Address() == ext.Address() {
			t.Fatal("rolled-back identity reused")
		}
		for _, a := range installed.Members {
			d := e.catalog.state.Get(a)
			if e.catalog.state.NameAddress(catalog.DescriptorNameKey(d)) != a {
				t.Fatal("committed signature identity lost")
			}
		}
	})
}

func extensionOwnershipImportUnits(t *testing.T) {
	e, _, ext := extensionUnitInstalled(t)
	// Actual isolated Restore stage supplies captured target-principal authority.
	stage := e.restoreStage()
	op, done := roleOperation(t, stage, "postgres")
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
	before := stage.catalog.state
	for _, kind := range []string{"namespace", "version", "member_owner", "duplicate", "member_identity"} {
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
		}
		if err := stage.importExtensionOwners(context.Background(), op, records); err == nil {
			t.Fatal("invalid ownership import accepted", kind)
		}
		if stage.catalog.state != before {
			t.Fatal("failed ownership import changed staged identities", kind)
		}
	}
	if err := stage.importExtensionOwners(context.Background(), op, []extension.OwnerRecord{record}); err != nil {
		t.Fatal(err)
	}
	// Central DROP dependencies include extension and every owned member, plus
	// the destination database's real grantor/grantee references.
	owner := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	var dependencies []access.RoleDependency
	access.DescriptorRoleDependencies(ext, owner, &dependencies)
	if len(dependencies) != 1 || dependencies[0].Address != ext.Address() || dependencies[0].Kind != 'o' || !access.DescriptorRoleDependencies(ext, owner, nil) {
		t.Fatal("extension owner dependency", dependencies)
	}
	for _, a := range ext.Members {
		d := e.catalog.state.Get(a)
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
	target := New()
	admin, adminDone := roleOperation(t, target, "postgres")
	defer adminDone()
	originalOwner := roleAttributeCreate(t, target, admin, "ext_import_owner", true, parser.RoleOptions{})
	imported := target.restoreStage()
	replay, replayDone := roleOperation(t, imported, "postgres")
	defer replayDone()
	if err := extensionUnitExecute(t, imported, replay, &parser.CreateExtension{Name: parser.Identifier{Text: "unaccent"}}); err != nil {
		t.Fatal(err)
	}
	ownerRecord := record
	ownerRecord.Members = append([]extension.MemberOwner(nil), record.Members...)
	for i := range ownerRecord.Members {
		ownerRecord.Members[i].Owner = originalOwner.Name
	}
	if err := imported.importExtensionOwners(context.Background(), replay, []extension.OwnerRecord{ownerRecord}); err != nil {
		t.Fatal(err)
	}
	roleAttributeApply(t, target, admin, &parser.DropRole{Names: []string{originalOwner.Name}})
	replacementOwner := roleAttributeCreate(t, target, admin, originalOwner.Name, true, parser.RoleOptions{})
	if replacementOwner.Ref == originalOwner.Ref {
		t.Fatal("target role identity reused")
	}
	incumbent := target.catalog.state
	if _, err := target.replaceCatalog(context.Background(), imported, imported.CatalogRead(), admin); err == nil {
		t.Fatal("member owner target recreation escaped import fence")
	}
	if target.catalog.state != incumbent {
		t.Fatal("member owner fence replaced target")
	}
}
