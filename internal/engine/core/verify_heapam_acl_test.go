package core_test

import (
	"context"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/parser"
)


func heapACLRecord(t *testing.T, metadata string) extension.OwnerRecord {
	t.Helper()
	record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(metadata, "-- ")))
	if err != nil || !found { t.Fatal("owner ACL codec", found, err) }
	return record
}

func TestVerifyHeapamACLMetadata(t *testing.T) {
	e, _, _ := typedUnitCatalog(t, false)
	ext := heapUnitInstalled(t, e)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	reader := roleAttributeCreate(t, e, op, "heap_acl_reader", false, parser.RoleOptions{})
	third := roleAttributeCreate(t, e, op, "heap_acl_third", false, parser.RoleOptions{})
	view := e.CatalogRead()
	proc := view.MustProcedure(ext.Members[0])
	if proc.Owner == reader.Ref || proc.Owner == third.Ref || reader.Ref == third.Ref { t.Fatal("fixture grant roles must be distinct") }
	ownerGrant := authorization.ACLEntry{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: proc.Owner}, Privileges: authorization.ExecutePrivilege}
	readerGrant := authorization.ACLEntry{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege}
	for _, tc := range []struct { name string; acl authorization.ACLValue; grantees []string; options []bool; public bool }{
		{"owner-baseline", authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{ownerGrant}}, []string{"postgres"}, []bool{false}, false},
		{"reader-grant", authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{ownerGrant, readerGrant}}, []string{"postgres", "heap_acl_reader"}, []bool{false, true}, false},
		{"nonowner-grantor", authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{ownerGrant, readerGrant, {Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: third.Ref}, Privileges: authorization.ExecutePrivilege}}}, []string{"postgres", "heap_acl_reader", "heap_acl_third"}, []bool{false, true, false}, false},
		{"revoked-reader", authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{ownerGrant}}, []string{"postgres"}, []bool{false}, false},
		{"explicit-empty", authorization.ACLValue{Explicit: true}, nil, nil, false},
		{"absent", authorization.ACLValue{}, nil, nil, false},
		{"public-grant", authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege}}}, []string{""}, []bool{false}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			captured := *proc
			captured.ACL = tc.acl
			state.SetDescriptor(proc.Address(), &captured)
			metadata, err := extension.OwnerMetadata(e.Catalog.Read(state), e.Roles(), ext)
			if err != nil { t.Fatal(err) }
			record := heapACLRecord(t, metadata)
			if len(record.Members) != 1 || record.Members[0].ACL == nil || record.Members[0].ACL.Explicit != tc.acl.Explicit { t.Fatal("ACL distinction lost", record) }
			if tc.name == "nonowner-grantor" {
				want := extension.MemberGrant{Grantor: "heap_acl_reader", Grantee: "heap_acl_third", Execute: true}
				if entries := record.Members[0].ACL.Entries; len(entries) != 3 || entries[2] != want { t.Fatal("nonowner grantor was not encoded by role name", entries, want) }
			}
			destination := e.RestoreStage()
			if tc.name == "nonowner-grantor" {
				destination, _, _ = typedUnitCatalog(t, false)
				rolesOp, rolesDone := enginetest.RoleOperation(t, destination, "postgres")
				defer rolesDone()
				roleAttributeCreate(t, destination, rolesOp, "heap_acl_spacer", false, parser.RoleOptions{})
				destinationThird := roleAttributeCreate(t, destination, rolesOp, "heap_acl_third", false, parser.RoleOptions{})
				destinationReader := roleAttributeCreate(t, destination, rolesOp, "heap_acl_reader", false, parser.RoleOptions{})
				if destinationReader.Ref == reader.Ref || destinationThird.Ref == third.Ref || destinationReader.Ref == destinationThird.Ref { t.Fatal("fixture did not create distinct destination-owned principals") }
			}
			replayOp, replayDone := enginetest.RoleOperation(t, destination, "postgres")
			defer replayDone()
			if err := extensionUnitExecute(t, destination, replayOp, &parser.CreateExtension{Name: parser.Identifier{Text: "amcheck"}}); err != nil { t.Fatal(err) }
			before := destination.CatalogRead()
			targetExt := before.MustExtension(before.NameAddress(catalog.DescriptorNameKey(ext)))
			candidate := catalog.CloneState(before.State())
			_, err = extension.ImportOwners(context.Background(), destination.Catalog.Read(candidate), destination.Roles(), []extension.OwnerRecord{record})
			if err != nil { t.Fatal(err) }
			got := candidate.MustProcedure(targetExt.Members[0]).ACL
			var want []authorization.ACLEntry
			owner, _ := destination.Roles().Lookup("postgres")
			for i, name := range tc.grantees {
				entry := authorization.ACLEntry{Grantor: owner.Ref, Privileges: authorization.ExecutePrivilege, Grantee: authorization.ACLGrantee{Public: tc.public}}
				if !tc.public { role, ok := destination.Roles().Lookup(name); if !ok { t.Fatal(name) }; entry.Grantee.Role = role.Ref }
				if tc.name == "nonowner-grantor" && name == "heap_acl_third" {
					grantor, ok := destination.Roles().Lookup("heap_acl_reader")
					if !ok { t.Fatal("destination grantor") }
					entry.Grantor = grantor.Ref
				}
				if tc.options[i] { entry.GrantOptions = authorization.ExecutePrivilege }
				want = append(want, entry)
			}
			if got.Explicit != tc.acl.Explicit || !reflect.DeepEqual(got.Entries, want) { t.Fatal("destination ACL mapping", got, want) }
			if before.MustProcedure(targetExt.Members[0]) == candidate.MustProcedure(targetExt.Members[0]) && tc.name != "owner-baseline" && tc.name != "revoked-reader" { t.Fatal("ACL import mutated or reused original descriptor") }
			if !reflect.DeepEqual(captured.ACL, tc.acl) || proc.ACL.Entries[0] != ownerGrant { t.Fatal("ACL import mutated source") }
		})
	}
}

func TestVerifyHeapamACLMetadataRejects(t *testing.T) {
	e, _, _ := typedUnitCatalog(t, false)
	ext := heapUnitInstalled(t, e)
	metadata, err := extension.OwnerMetadata(e.CatalogRead(), e.Roles(), ext)
	if err != nil { t.Fatal(err) }
	for _, payload := range []string{
		`{"explicit":true,"entries":null}`,
		`{"explicit":true,"explicit":false,"entries":[]}`,
		`{"explicit":true,"entries":[],"unknown":true}`,
		`{"explicit":false,"entries":[{"grantor":"postgres","grantee":"postgres","public":false,"execute":true,"grant_option":false}]}`,
		`{"explicit":true,"entries":[{"grantor":"postgres","grantee":"","public":true,"execute":true,"grant_option":true}]}`,
	} {
		jsonBytes, err := base64.StdEncoding.DecodeString(strings.Fields(metadata)[2])
		if err != nil { t.Fatal(err) }
		// Replace exactly the generated final ACL value, keeping the genuine
		// installation/member identity and all surrounding strict fields.
		text := string(jsonBytes)
		start := strings.Index(text, `"acl":`)
		if start < 0 { t.Fatal("missing generated ACL") }
		text = text[:start]+`"acl":`+payload+`}]}`
		_, found, err := extension.DecodeOwners(extension.OwnersMarker+" "+base64.StdEncoding.EncodeToString([]byte(text)))
		if !found || err == nil { t.Fatal("malformed ACL accepted", payload, err) }
	}
	for _, mutate := range []func(*extension.OwnerRecord){
		func(record *extension.OwnerRecord) { record.Members[0].Slot = 1 },
		func(record *extension.OwnerRecord) { record.Members[0].Name = "foreign_same_signature" },
		func(record *extension.OwnerRecord) { record.Members[0].ACL = nil },
		func(record *extension.OwnerRecord) { record.Members[0].ACL.Entries[0].Grantor = "heap_missing_grantor" },
		func(record *extension.OwnerRecord) { record.Members[0].ACL.Entries[0].Grantee = "heap_missing_grantee" },
	} {
		record := heapACLRecord(t, metadata)
		mutate(&record)
		before := e.CatalogRead()
		candidate := catalog.CloneState(before.State())
		if _, err := extension.ImportOwners(context.Background(), e.Catalog.Read(candidate), e.Roles(), []extension.OwnerRecord{record}); err == nil { t.Fatal("forged slot/role authority accepted", record) }
		if e.Catalog.State != before.State() { t.Fatal("failed detached ACL import published") }
	}
}
