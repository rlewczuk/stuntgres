package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestPGInitPrivsNativeMembershipReplayScope(t *testing.T) {
	e := New()
	run(t, e, `CREATE EXTENSION unaccent`)
	run(t, e, `REVOKE EXECUTE ON FUNCTION public.unaccent(text) FROM PUBLIC`)
	var dump bytes.Buffer
	if err := Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	var records []extension.OwnerRecord
	for _, line := range strings.Split(dump.String(), "\n") {
		record, recognized, err := extension.DecodeOwners(strings.TrimPrefix(line, "-- "))
		if err != nil {
			t.Fatal(err)
		}
		if recognized {
			records = append(records, record)
		}
	}
	view := e.CatalogRead()
	ext, ok := view.Extension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "unaccent"}))
	if !ok || len(records) == 0 {
		t.Fatal("native installation recipe missing")
	}
	var root catalog.Descriptor
	for _, descriptor := range view.Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok && p.Name() == "unaccent" {
			root = p
			break
		}
	}
	if root == nil || !control.NativeMembershipHistoryReplay(view, records, root, ext, "drop") {
		t.Fatal("native installed DROP repair not recognized")
	}
	detached, err := catalog.WithExtensionMembership(root, ids.ObjectAddress{}, root.Revision())
	if err != nil {
		t.Fatal(err)
	}
	state := catalog.CloneState(view.State())
	state.SetDescriptor(root.Address(), detached)
	addView := view.WithState(state)
	if !control.NativeMembershipHistoryReplay(addView, records, detached, ext, "add") {
		t.Fatal("native installed ADD repair not recognized")
	}
	for _, test := range []struct {
		name    string
		records []extension.OwnerRecord
		root    catalog.Descriptor
		target  *catalog.Extension
		action  string
	}{
		{"plain SQL", nil, detached, ext, "add"},
		{"wrong action", records, detached, ext, "schema"},
		{"attached ADD", records, root, ext, "add"},
		{"detached DROP", records, detached, ext, "drop"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if control.NativeMembershipHistoryReplay(addView, test.records, test.root, test.target, test.action) {
				t.Fatal("unmatched action borrowed native repair authority")
			}
		})
	}
	wrongExtension := *ext
	wrongExtension.ObjectName = "ip_wrong_extension"
	if control.NativeMembershipHistoryReplay(addView, records, detached, &wrongExtension, "add") {
		t.Fatal("wrong extension borrowed repair authority")
	}
	forged := append([]extension.OwnerRecord(nil), records...)
	for i := range forged {
		forged[i].Members = append([]extension.MemberOwner(nil), forged[i].Members...)
		for j := range forged[i].Members {
			member := &forged[i].Members[j]
			if member.Declaration != nil {
				declaration := *member.Declaration + 10000
				member.Declaration = &declaration
			}
		}
	}
	if control.NativeMembershipHistoryReplay(addView, forged, detached, ext, "add") || control.NativeMembershipHistoryReplay(view, forged, root, ext, "drop") {
		t.Fatal("forged provider declaration borrowed repair authority")
	}
	moved := *detached.(*catalog.Procedure)
	moved.ObjectName = "ip_unmatched_identity"
	movedState := catalog.CloneState(state)
	movedState.SetDescriptor(moved.Address(), &moved)
	if control.NativeMembershipHistoryReplay(view.WithState(movedState), records, &moved, ext, "add") {
		t.Fatal("unmatched final identity borrowed ADD repair authority")
	}
	// The native recipe grants each exact root/extension/action repair only
	// once. A subsequently appended genuine DROP+ADD must capture current ACL,
	// rather than borrowing authority from the earlier metadata declaration.
	if strings.Contains(dump.String(), control.InitialPrivilegesMarker) {
		t.Fatal("current-only unaccent mutation invented source history")
	}
	appended := dump.String() + "ALTER EXTENSION unaccent DROP FUNCTION public.unaccent(text);\nALTER EXTENSION unaccent ADD FUNCTION public.unaccent(text);\n"
	target := New()
	if err := Restore(context.Background(), target, strings.NewReader(appended)); err != nil {
		t.Fatal(err)
	}
	address := target.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "unaccent", Signature: "25"})
	if record := target.Catalog.State.InitialPrivilege(address); record == nil || record.PrivType != 'e' {
		t.Fatal("appended real membership event reused consumed native repair authority", record)
	}
}
