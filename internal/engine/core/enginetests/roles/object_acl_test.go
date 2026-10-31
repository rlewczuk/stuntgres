package roles_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func TestObjectACLAddressLookup(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	whole := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, owner, authorization.SelectPrivilege, 0)}}
	column := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: 1259, ObjectID: 48001}, Attributes: []int32{1, 3}, Security: catalog.RelationSecurity{ACL: whole, ColumnACL: map[int32]authorization.ACLValue{1: column, 2: whole}}}
	view = view.WithState(catalog.CloneState(view.State()))
	view.State().SetDescriptor(relation.Address(), relation)
	schema := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: 2615, ObjectID: 48002}, ACL: whole}
	view.State().SetDescriptor(schema.Address(), schema)
	check := func(address ids.ObjectAddress, want authorization.ACLValue) {
		t.Helper()
		got, err := access.RawObjectACL(view, address)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("address %#v got %#v want %#v: %v", address, got, want, err)
		}
	}
	for _, class := range []ids.OID{1259, 2615, 1247, 1255, 1262, 2612, 1213, 2328, 1417, 2995, 2613} {
		hasACL, known := access.ObjectACLClass(class)
		if !hasACL || !known {
			t.Fatal("released ACL-bearing class", class, hasACL, known)
		}
		check(ids.ObjectAddress{ClassID: class}, authorization.ACLValue{})
	}
	// Independently enumerated REL_18_0 ObjectProperty ACL-less classes.
	for _, class := range []ids.OID{2601, 2602, 2603, 2605, 3456, 2606, 2607, 826, 3079, 2616, 2617, 2753, 1260, 1261, 2618, 3576, 2620, 3256, 3466, 3602, 3600, 3601, 3764, 6104, 6100, 3381, 1418} {
		hasACL, known := access.ObjectACLClass(class)
		if hasACL || !known {
			t.Fatal("released ACL-less class", class, hasACL, known)
		}
		check(ids.ObjectAddress{ClassID: class, ObjectID: 999999, SubID: -1}, authorization.ACLValue{})
	}
	for _, class := range []ids.OID{0, 1249, 6243, 999999} {
		if hasACL, known := access.ObjectACLClass(class); hasACL || known {
			t.Fatal("unregistered class", class)
		}
		_, err := access.RawObjectACL(view, ids.ObjectAddress{ClassID: class, ObjectID: 1})
		if err == nil {
			t.Fatal("unknown class became NULL", class)
		}
	}
	for _, subid := range []int32{0, 1, -1, 65537} {
		check(ids.ObjectAddress{SubID: subid}, authorization.ACLValue{})
		check(ids.ObjectAddress{ClassID: 2615, ObjectID: 48002, SubID: subid}, whole)
	}
	check(relation.Address(), whole)
	for _, subid := range []int32{1, 65537, -65535} {
		check(ids.ObjectAddress{ClassID: 1259, ObjectID: 48001, SubID: subid}, column)
	}
	for _, subid := range []int32{2, 3, 4, -1, 65536, -65536} {
		check(ids.ObjectAddress{ClassID: 1259, ObjectID: 48001, SubID: subid}, authorization.ACLValue{})
	}
	roles := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{})
	array, _ := view.LookupType(sqltype.ACLItemArray)
	for _, acl := range []authorization.ACLValue{{}, {Explicit: true}, column} {
		value, err := pgcatalog.ClassACL(&e.Catalog.Owner, roles, array, acl)
		if err != nil || value.Type() != array || value.IsNull() == acl.Explicit || value.ArrayLen() != len(acl.Entries) {
			t.Fatal("typed raw ACL conversion", value, err)
		}
	}
	var foreign authorization.Domain
	bad := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(foreign.Principal(10), owner, authorization.SelectPrivilege, 0)}}
	if _, err := pgcatalog.ClassACL(&e.Catalog.Owner, roles, array, bad); err == nil {
		t.Fatal("foreign principal escaped checked ACLITEM constructor")
	}
	t.Run("explicit_sequence_acl", func(t *testing.T) {
		// Use actual descriptor staging and the existing ACL publication seam;
		// no sequence GRANT SQL or new producer is introduced by this inquiry.
		eng := engine.New()
		catalogView := eng.CatalogRead()
		principal := eng.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		stage := eng.BeginCatalogStage(catalogView)
		options, _, err := sequence.BindOptions(nil, storage.SequenceGenerator{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		eng.Mu.Lock()
		columns := sequence.Columns(eng.Catalog.Current())
		sequence, err := table.NewRelation(core.DDLHost{E: eng}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "acl_unit_sequence", principal, columns, nil)
		eng.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		sequence.RelationKind, sequence.Sequence = catalog.SequenceRelation, &options
		sequence.SequencePlanStamp = sequence.Revision()
		if err := stage.Create(sequence); err != nil {
			t.Fatal(err)
		}
		enginetest.PublishStage(t, eng, stage)
		ref := eng.Catalog.Domain.Ref(eng.CatalogRead().Epoch(), sequence.Address())
		want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			objectACLUnitEntry(principal, principal, authorization.SelectPrivilege|authorization.UpdatePrivilege|authorization.UsagePrivilege, 0),
			{Grantor: principal, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
		}}
		if err := enginetest.SetObjectACL(eng, context.Background(), ref, want); err != nil {
			t.Fatal(err)
		}
		got, err := access.RawObjectACL(eng.CatalogRead(), sequence.Address())
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("sequence raw ACL", got, want, err)
		}
	})
}

func objectACLUnitEntry(source, target authorization.PrincipalRef, use, options authorization.Privileges) authorization.ACLEntry {
	return authorization.ACLEntry{Grantor: source, Grantee: authorization.ACLGrantee{Role: target}, Privileges: use, GrantOptions: options}
}
