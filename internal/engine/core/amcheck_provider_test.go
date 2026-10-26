package core_test

import (
	"context"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func amcheckUnitInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION amcheck")
	return e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"}))
}

func TestAmcheckProviderManifest(t *testing.T) {
	e := engine.New()
	ext := amcheckUnitInstalled(t, e)
	view := e.CatalogRead()
	control := extension.StockAmcheck.Control()
	if control.Version != "1.5" || control.Trusted || !control.Superuser || !control.Relocatable || control.Comment == nil || *control.Comment != "functions for verifying relation integrity" {
		t.Fatal(control)
	}
	if err := extension.StockAmcheck.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	if len(ext.Members) != 8 {
		t.Fatal(ext.Members)
	}
	wantNames := [...]string{"bt_index_check", "bt_index_check", "bt_index_check", "bt_index_parent_check", "bt_index_parent_check", "bt_index_parent_check", "bt_index_parent_check"}
	wantSignatures := [...]catalog.ProcedureSignature{"2205", "2205,16", "2205,16,16", "2205", "2205,16", "2205,16,16", "2205,16,16,16"}
	wantArguments := [...][4]string{{"index"}, {"index", "heapallindexed"}, {"index", "heapallindexed", "checkunique"}, {"index"}, {"index", "heapallindexed"}, {"index", "heapallindexed", "rootdescend"}, {"index", "heapallindexed", "rootdescend", "checkunique"}}
	for slot, address := range ext.Members[1:] {
		p := view.MustProcedure(address)
		if p.Name() != wantNames[slot] || p.Signature != wantSignatures[slot] || p.Result != 2278 || p.Volatility != expr.Volatile || !p.Strict || p.Installed.ReturnsSet || p.Installed.Parallel != 'r' || p.Installed.ArgumentNames != wantArguments[slot] || p.Owner != ext.Owner || p.Namespace() != ext.TargetNamespace {
			t.Fatal("release declaration", slot, p)
		}
		if !p.ACL.Explicit || len(p.ACL.Entries) != 1 || p.ACL.Entries[0] != (authorization.ACLEntry{Grantor: ext.Owner, Grantee: authorization.ACLGrantee{Role: ext.Owner}, Privileges: authorization.ExecutePrivilege}) {
			t.Fatal("owner-only EXECUTE", p.ACL)
		}
		for _, acl := range []authorization.ACLValue{
			{},
			{Explicit: true},
			{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: p.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege}}},
			{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: p.Owner, Grantee: authorization.ACLGrantee{Role: p.Owner}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege}}},
		} {
			state := catalog.CloneState(view.State())
			altered := *p
			altered.ACL = acl
			state.SetDescriptor(address, &altered)
			metadata, err := extension.OwnerMetadata(e.Catalog.Read(state), e.Roles(), ext)
			if err != nil {
				t.Fatal("lawful B-tree ACL not replayable", acl, err)
			}
			record := heapACLRecord(t, metadata)
			if len(record.Members) != 8 {
				t.Fatal("ACL metadata lost composed members", record)
			}
			destination := catalog.CloneState(view.State())
			if _, err := extension.ImportOwners(context.Background(), e.Catalog.Read(destination), e.Roles(), []extension.OwnerRecord{record}); err != nil {
				t.Fatal("lawful B-tree ACL replay", acl, err)
			}
			if got := destination.MustProcedure(address).ACL; got.Explicit != acl.Explicit || !reflect.DeepEqual(got.Entries, acl.Entries) {
				t.Fatal("B-tree ACL distinction lost", got, acl)
			}
			if !reflect.DeepEqual(view.MustProcedure(address).ACL, p.ACL) {
				t.Fatal("ACL replay mutated source")
			}
		}
		ref := e.Catalog.Domain.Ref(view.Epoch(), address)
		if err := extension.ValidateAmcheckIdentity(view, p, ref); err != nil {
			t.Fatal(err)
		}
		ref.Epoch++
		if err := extension.ValidateAmcheckIdentity(view, p, ref); err == nil {
			t.Fatal("stale epoch admitted")
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Strict = false },
			func(p *catalog.Procedure) { p.Signature = "23" },
			func(p *catalog.Procedure) { p.Result = 25 },
			func(p *catalog.Procedure) { p.Volatility = expr.Stable },
			func(p *catalog.Procedure) { p.ObjectRevision = 0 },
			func(p *catalog.Procedure) { p.Implementation = new(extension.AmcheckProvider) },
			func(p *catalog.Procedure) { p.Extension = ids.ObjectAddress{} },
			func(p *catalog.Procedure) { p.Installed.Parallel = 's' },
			func(p *catalog.Procedure) { p.Installed.ReturnsSet = true },
			func(p *catalog.Procedure) { p.Installed.ArgumentNames[0] = "other" },
			func(p *catalog.Procedure) { p.Display.Source = "other" },
		} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(address, &bad)
			if err := extension.StockAmcheck.ValidateExtension(e.Catalog.Read(state), ext); err == nil {
				t.Fatal("forged declaration accepted", slot, bad)
			}
		}
	}
	state := catalog.CloneState(view.State())
	bad := *ext
	bad.Members = append([]ids.ObjectAddress(nil), ext.Members...)
	bad.Members[0], bad.Members[3] = bad.Members[3], bad.Members[0]
	state.SetDescriptor(ext.Address(), &bad)
	if err := extension.StockAmcheck.ValidateExtension(e.Catalog.Read(state), &bad); err == nil {
		t.Fatal("member order reinterpreted implementation")
	}
}
