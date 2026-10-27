package rolecmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/largeobject"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/rolecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func largeOwnerFixture(t *testing.T) (*core.Engine, *catalog.LargeObject, authorization.PrincipalRef, authorization.PrincipalRef, authorization.PrincipalRef) {
	t.Helper()
	e, grantor, source, target, reader := ownershipUnitRoles()
	content, err := (largeobject.Value{}).Write(0, "\x00\x01\x7f\x80\xff\x00")
	if err == nil { content, err = content.Write(4294967296, "\x00\xff\x00") }
	if err != nil { t.Fatal(err) }
	selectRight, updateRight := authorization.SelectPrivilege, authorization.UpdatePrivilege
	old := &catalog.LargeObject{
		ObjectAddress: ids.ObjectAddress{ClassID: ids.LargeObjectClassID, ObjectID: 910201},
		ObjectName: "910201", ObjectRevision: 11, Owner: source, Content: content,
		ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			objectACLUnitEntry(source, source, selectRight, 0),
			objectACLUnitEntry(target, target, updateRight, updateRight),
			objectACLUnitEntry(reader, source, selectRight, selectRight),
			objectACLUnitEntry(reader, target, updateRight, 0),
			{Grantor: source, Grantee: authorization.ACLGrantee{Public: true}, Privileges: selectRight},
			objectACLUnitEntry(grantor, reader, updateRight, 0),
			objectACLUnitEntry(target, reader, 0, 0),
		}},
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.Create(old); err != nil { t.Fatal(err) }
	ownershipUnitPublish(t, e, stage)
	return e, old, target, reader, grantor
}

func largeOwnerContentIdentity(t *testing.T, got, want largeobject.Value) {
	t.Helper()
	// Distinguish a shallow Value copy from reconstructing the same bytes or
	// cloning its sparse map. Chunks also witnesses every borrowed page backing.
	if reflect.ValueOf(got).FieldByName("pages").UnsafePointer() != reflect.ValueOf(want).FieldByName("pages").UnsafePointer() {
		t.Fatal("ownership copied the sparse content map")
	}
	pages := map[int64][]byte{}
	want.Chunks(func(offset int64, data []byte) bool { pages[offset] = data; return true })
	count := 0
	got.Chunks(func(offset int64, data []byte) bool {
		count++
		old := pages[offset]
		if len(data) != len(old) || len(data) == 0 || &data[0] != &old[0] || !reflect.DeepEqual(data, old) { t.Fatal("ownership copied or changed a sparse page", offset) }
		return true
	})
	if count != len(pages) || !reflect.DeepEqual(got, want) { t.Fatal("ownership changed sparse layout or EOF") }
}

func TestLargeObjectOwnerStage(t *testing.T) {
	for _, shape := range []string{"null", "empty", "collisions", "same-owner", "revision-headroom"} {
		t.Run(shape, func(t *testing.T) {
			e, original, target, reader, grantor := largeOwnerFixture(t)
			old := original
			if shape == "null" || shape == "empty" || shape == "revision-headroom" {
				next := *original
				next.ObjectRevision++
				if shape == "revision-headroom" {
					next.ObjectRevision = ids.DefinitionRevision(^uint64(0))
				} else {
					next.ACL = authorization.ACLValue{Explicit: shape == "empty"}
				}
				setup := e.BeginCatalogStage(e.CatalogRead())
				if err := setup.Replace(&next); err != nil { t.Fatal(err) }
				ownershipUnitPublish(t, e, setup)
				old = &next
			}
			base := e.CatalogRead()
			retained := append([]authorization.ACLEntry(nil), old.ACL.Entries...)
			stage := e.BeginCatalogStage(base)
			defer e.DiscardCatalogStage(stage)
			if shape == "revision-headroom" {
				if err := role.StageOwnership(stage, old, target); err == nil { t.Fatal("exhausted revision accepted") }
				if stage.DescriptorCount() != 0 || e.CatalogRead().Get(old.Address()) != old || !reflect.DeepEqual(old.ACL.Entries, retained) { t.Fatal("revision exhaustion changed descriptor/ACL") }
				largeOwnerContentIdentity(t, old.Content, original.Content)
				return
			}
			to := target
			if shape == "same-owner" { to = old.Owner }
			if err := role.StageOwnership(stage, old, to); err != nil { t.Fatal(err) }
			if shape == "same-owner" {
				if stage.DescriptorCount() != 0 || base.Get(old.Address()) != old { t.Fatal("same owner staged a replacement") }
				return
			}
			next := stage.Get(old.Address()).(*catalog.LargeObject)
			if next == old || next.Owner != target || next.Revision() != old.Revision()+1 || next.Address() != old.Address() || next.Name() != old.Name() { t.Fatal("descriptor identity/revision", next) }
			largeOwnerContentIdentity(t, next.Content, old.Content)
			want := authorization.ACLValue{Explicit: shape != "null"}
			if shape == "collisions" {
				want.Entries = []authorization.ACLEntry{
					objectACLUnitEntry(target, target, authorization.SelectPrivilege|authorization.UpdatePrivilege, authorization.UpdatePrivilege),
					objectACLUnitEntry(reader, target, authorization.SelectPrivilege|authorization.UpdatePrivilege, authorization.SelectPrivilege),
					{Grantor: target, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege},
					objectACLUnitEntry(grantor, reader, authorization.UpdatePrivilege, 0),
				}
			}
			if !reflect.DeepEqual(next.ACL, want) { t.Fatalf("ordered ACL=%#v want=%#v", next.ACL, want) }
			if len(next.ACL.Entries) > 0 && &next.ACL.Entries[0] == &old.ACL.Entries[0] { t.Fatal("ACL replacement aliases retained entries") }
			if !reflect.DeepEqual(old.ACL.Entries, retained) || old.Owner != original.Owner || base.Get(old.Address()) != old { t.Fatal("staging mutated retained descriptor") }
			// Re-staging the already-private owner is a true no-op.
			if err := role.StageOwnership(stage, next, target); err != nil { t.Fatal(err) }
			if stage.Get(old.Address()) != next || stage.DescriptorCount() != 1 { t.Fatal("same private owner restaged") }
			ownershipUnitPublish(t, e, stage)
			if e.CatalogRead().Get(old.Address()) != next || base.Get(old.Address()) != old { t.Fatal("publication mutated old root") }
			largeOwnerContentIdentity(t, old.Content, next.Content)
		})
	}
}

func largeOwnerDependencies(d catalog.Descriptor, ref authorization.PrincipalRef) []access.RoleDependency {
	if d == nil { return nil }
	var deps []access.RoleDependency
	access.DescriptorRoleDependencies(d, ref, &deps)
	return deps
}

func TestLargeObjectOwnerDependencies(t *testing.T) {
	e, old, target, reader, grantor := largeOwnerFixture(t)
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := role.StageOwnership(stage, old, target); err != nil { t.Fatal(err) }
	next := stage.Get(old.Address())
	ownerDependency := []access.RoleDependency{{Address: old.Address(), Kind: 'o'}}
	aclDependency := []access.RoleDependency{{Address: old.Address(), Kind: 'a'}}
	for _, tc := range []struct{ ref authorization.PrincipalRef; before, after []access.RoleDependency }{
		{old.Owner, ownerDependency, nil}, {target, aclDependency, ownerDependency},
		{reader, aclDependency, aclDependency}, {grantor, aclDependency, aclDependency},
	} {
		if got := largeOwnerDependencies(old, tc.ref); !reflect.DeepEqual(got, tc.before) { t.Fatal("before", tc.ref, got, tc.before) }
		if got := largeOwnerDependencies(next, tc.ref); !reflect.DeepEqual(got, tc.after) { t.Fatal("after", tc.ref, got, tc.after) }
	}
}

func TestLargeObjectOwnerNumeric(t *testing.T) {
	e, old, _, _, _ := largeOwnerFixture(t)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	e.Mu.Unlock()
	if err != nil { t.Fatal(err) }
	host := rolecmds.RoleHost{DDLHost: core.DDLHost{E: e, Op: op}}
	for _, tc := range []struct{ number parser.Value; code, message string }{
		{parser.Value{Kind: parser.IntegerValue, Integer: -1}, "42704", "large object 4294967295 does not exist"},
		{parser.Value{Kind: parser.NumericValue, Text: "2147483648"}, "42704", "large object 2147483648 does not exist"},
		{parser.Value{Kind: parser.NumericValue, Text: "-2147483648"}, "42704", "large object 2147483648 does not exist"},
		{parser.Value{Kind: parser.NumericValue, Text: "0xFFFFFFFF"}, "42704", "large object 4294967295 does not exist"},
		{parser.Value{Kind: parser.NumericValue, Text: "4294967296"}, "22003", `value "4294967296" is out of range for type oid`},
		{parser.Value{Kind: parser.NumericValue, Text: "-2147483649"}, "22003", `value "-2147483649" is out of range for type oid`},
		{parser.Value{Kind: parser.NumericValue, Text: "4_294_967_295"}, "22P02", `invalid input syntax for type oid: "4_294_967_295"`},
		{parser.Value{Kind: parser.NumericValue, Text: "1.0"}, "22P02", `invalid input syntax for type oid: "1.0"`},
	} {
		statement := &parser.AlterOwner{Kind: "large object", Number: tc.number}
		e.Mu.Lock()
		_, err := role.PlanLargeObjectOwnership(host, auth, statement, "ownership_target", false)
		e.Mu.Unlock()
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Code != tc.code || d.Message != tc.message || d.Position != "" { t.Fatal(tc.number, err) }
		// Even invalid OID text must not hide the missing-recipient error.
		e.Mu.Lock()
		_, err = role.PlanLargeObjectOwnership(host, auth, statement, "missing_owner", false)
		e.Mu.Unlock()
		d, ok = err.(*sqlstate.Error)
		if !ok || d.Code != "42704" || d.Message != `role "missing_owner" does not exist` { t.Fatal(err) }
	}
	statement := &parser.AlterOwner{Kind: "large object", Number: parser.Value{Kind: parser.IntegerValue, Integer: int64(old.Address().ObjectID)}}
	before, roles := e.Catalog.State, e.RoleStore.Published
	e.Mu.Lock()
	change, err := role.PlanLargeObjectOwnership(host, auth, statement, "ownership_source", false)
	e.Mu.Unlock()
	if err != nil || change.Object != nil || e.Catalog.State != before || e.RoleStore.Published != roles { t.Fatal("same owner resolved a mutation", change, err) }
	// Describe must not perform even numeric input or recipient lookup.
	description, err := e.Describe(op, &parser.AlterOwner{Kind: "large object", Number: parser.Value{Kind: parser.NumericValue, Text: "1.0"}, Target: parser.RoleSpec{Name: "missing_owner"}})
	if err != nil || description.Parameters.Count != 0 || len(description.Parameters.Types) != 0 || len(description.Results) != 0 || e.Catalog.State != before { t.Fatal("effectful description", description, err) }
}
