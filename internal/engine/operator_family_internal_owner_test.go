package engine

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestOperatorFamilyInternalOwners(t *testing.T) {
	t.Run("physical_selector", func(t *testing.T) {
		e := New()
		state := catalog.CloneState(e.CatalogRead().State())
		ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
		method := &catalog.AccessMethod{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 97000}, ObjectName: "owner_alias", ObjectRevision: 1, Native: catalog.BtreeAccessMethod, Handler: 330}
		family := &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 97001}, ObjectNamespace: ns, ObjectName: "owner_alias_family", ObjectRevision: 1, Method: 97000}
		class := &catalog.OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: 97002}, ObjectNamespace: ns, ObjectName: "owner_alias_class", ObjectRevision: 1, Method: 97000, Family: 97001, InputType: 23}
		for _, d := range []catalog.Descriptor{method, family, class} {
			state.SetDescriptor(d.Address(), d)
			state.BindName(catalog.DescriptorNameKey(d), d.Address())
		}
		read := e.CatalogRead().WithState(state)
		builtin := ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: 1978}
		for _, tc := range []struct {
			method, family, left, right ids.OID
			number                      int16
			function                    bool
			creating                    *catalog.OperatorClass
			want                        ids.ObjectAddress
		}{
			{403, 1976, 23, 23, 1, true, nil, builtin},
			{403, 1976, 23, 23, 5, false, nil, builtin},
			{403, 1976, 23, 23, 2, true, nil, ids.ObjectAddress{}},
			{403, 1976, 21, 23, 1, true, nil, ids.ObjectAddress{}},
			{97000, 97001, 23, 23, 1, true, nil, ids.ObjectAddress{}},
			{97000, 97001, 23, 23, 1, true, class, class.Address()},
			{97000, 97001, 23, 23, 2, true, class, ids.ObjectAddress{}},
		} {
			got := catalog.FamilyMemberInternalOwner(read, tc.method, tc.family, tc.left, tc.right, tc.number, tc.function, tc.creating)
			if got != tc.want {
				t.Fatal("physical-vs-SQL method owner", tc, got)
			}
		}
		gistFamily := ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 97003}
		gistClass := *class
		gistClass.Method = 783
		gistClass.Family = gistFamily.ObjectID
		for _, number := range []int16{1, 2, 5, 6, 7} {
			if got := catalog.FamilyMemberInternalOwner(read, 783, gistFamily.ObjectID, 23, 23, number, true, nil); got != gistFamily {
				t.Fatal("ALTER required GiST owner", number, got)
			}
			if got := catalog.FamilyMemberInternalOwner(read, 783, gistFamily.ObjectID, 23, 23, number, true, &gistClass); got != gistClass.Address() {
				t.Fatal("CREATE required GiST owner", number, got)
			}
			if got := catalog.FamilyMemberInternalOwner(read, 783, gistFamily.ObjectID, 23, 23, number, false, &gistClass); got != (ids.ObjectAddress{}) {
				t.Fatal("GiST operator acquired hard owner", number, got)
			}
		}
		for _, number := range []int16{3, 4, 8, 9, 10, 11} {
			if got := catalog.FamilyMemberInternalOwner(read, 783, gistFamily.ObjectID, 23, 23, number, true, &gistClass); got != (ids.ObjectAddress{}) {
				t.Fatal("optional GiST support acquired hard owner", number, got)
			}
		}
	})
	t.Run("stored_owners_dump_and_retirement", func(t *testing.T) {
		e := New()
		owner := sessionStart(t, e, nil)
		var baseline bytes.Buffer
		if err := Dump(context.Background(), e, &baseline); err != nil {
			t.Fatal(err)
		}
		ref := transactionBegin(t, owner)
		for _, q := range []string{
			`CREATE OPERATOR FAMILY public.owner_gist USING gist`,
			`CREATE OPERATOR CLASS public.owner_gist_class FOR TYPE integer USING gist FAMILY public.owner_gist AS FUNCTION 2 pg_catalog.int4mi(integer,integer)`,
			`ALTER OPERATOR FAMILY public.owner_gist USING gist ADD FUNCTION 1 (integer,integer) pg_catalog.int4mi(integer,integer), FUNCTION 3 (integer,integer) pg_catalog.int4mi(integer,integer)`,
		} {
			definitionUnitStage(t, e, owner, q)
		}
		private := definitionUnitRead(e, owner)
		familyAddress := private.NameAddress(catalog.FamilyNameKey(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, 783, "owner_gist"))
		class, _ := private.LookupOperatorClass(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, 783, "owner_gist_class")
		check := func(read catalog.Read, familyAddress, classAddress ids.ObjectAddress) {
			t.Helper()
			for number, want := range map[int16]ids.ObjectAddress{1: familyAddress, 2: classAddress, 3: {}} {
				member, ok := read.FamilySupport(familyAddress.ObjectID, 23, 23, number)
				if !ok || member.InternalOwner != want || member.Function != 181 {
					t.Fatal("stored insertion-time owner", number, member, want)
				}
				if err := (catalog.OperatorFamilyProvider{Class: ids.AccessMethodProcedureClassID}).Validate(read, member); err != nil {
					t.Fatal(err)
				}
				deps := catalog.OperatorFamilyDependencies(read, member)
				kind, implementation := 'i', 'n'
				if want == (ids.ObjectAddress{}) {
					want = familyAddress
					kind, implementation = 'a', 'a'
				}
				if len(deps) != 2 || deps[0].Address != want || deps[0].Kind != byte(kind) || deps[1].Address != (ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 181}) || deps[1].Kind != byte(implementation) {
					t.Fatal("owner dependency flavors", number, deps)
				}
			}
		}
		check(private, familyAddress, class.Address())
		required, _ := private.FamilySupport(familyAddress.ObjectID, 23, 23, 1)
		classOwned, _ := private.FamilySupport(familyAddress.ObjectID, 23, 23, 2)
		for _, member := range []*catalog.OperatorFamilySupport{required, classOwned} {
			for _, badOwner := range []ids.ObjectAddress{{ClassID: ids.RelationClassID, ObjectID: familyAddress.ObjectID}, {ClassID: ids.OperatorFamilyClassID, ObjectID: 1976}, {ClassID: ids.OperatorClassCatalogID, ObjectID: 1978}, {ClassID: ids.OperatorFamilyClassID, ObjectID: familyAddress.ObjectID, SubID: 1}} {
				bad := *member
				bad.InternalOwner = badOwner
				if err := (catalog.OperatorFamilyProvider{Class: ids.AccessMethodProcedureClassID}).Validate(private, &bad); err == nil {
					t.Fatal("invalid internal owner accepted", badOwner)
				}
			}
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{familyAddress: {ClassID: ids.OperatorFamilyClassID, ObjectID: 99000}, class.Address(): {ClassID: ids.OperatorClassCatalogID, ObjectID: 99001}}
			remapped := catalog.RemapOperatorFamily(member, mapping).(*catalog.OperatorFamilySupport)
			if remapped.Family != 99000 || remapped.InternalOwner != mapping[member.InternalOwner] || catalog.OperatorFamilyMemberOwner(member) != member.InternalOwner {
				t.Fatal("typed owner remap", remapped)
			}
		}
		var excluded bytes.Buffer
		if err := Dump(context.Background(), e, &excluded); err != nil || !bytes.Equal(baseline.Bytes(), excluded.Bytes()) {
			t.Fatal("Dump exposed private family ownership", err)
		}
		transactionEnd(t, owner, ref, true)
		check(e.CatalogRead(), familyAddress, class.Address())
		var encoded bytes.Buffer
		if err := Dump(context.Background(), e, &encoded); err != nil {
			t.Fatal(err)
		}
		restored := New()
		if err := Restore(context.Background(), restored, bytes.NewReader(encoded.Bytes())); err != nil {
			t.Fatal("hard-family member must replay as ALTER FAMILY", err)
		}
		read := restored.CatalogRead()
		restoredFamily := read.NameAddress(catalog.FamilyNameKey(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, 783, "owner_gist"))
		restoredClass, _ := read.LookupOperatorClass(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, 783, "owner_gist_class")
		if restoredClass == nil || restoredFamily == (ids.ObjectAddress{}) {
			t.Fatal("roundtrip lost definition identities")
		}
		check(read, restoredFamily, restoredClass.Address())
		definitionUnitStage(t, e, owner, `DROP OPERATOR CLASS public.owner_gist_class USING gist RESTRICT`)
		after := e.CatalogRead()
		if after.Get(classOwned.Address()) != nil || after.Get(required.Address()) != required {
			t.Fatal("class retirement confused family ownership")
		}
		definitionUnitStage(t, e, owner, `DROP OPERATOR FAMILY public.owner_gist USING gist RESTRICT`)
		for _, d := range []catalog.Descriptor{required, classOwned} {
			if e.CatalogRead().Get(d.Address()) != nil {
				t.Fatal("whole-family closure left member", d)
			}
		}
		if e.CatalogRead().Get(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 181}) == nil {
			t.Fatal("AUTO/INTERNAL closure retired implementation")
		}
		check(private, familyAddress, class.Address())
	})
}
