package enginetests_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGShdependProducerAuthority(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := policyUnitInput(t, f, view)
	in.Source = view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.ShdependOID})
	in.Ref = view.Owner().Domain.Ref(view.Epoch(), in.Source.Address())
	produce := registeredCatalogProducer(pgcatalog.ShdependOID)
	rows, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil || rows == nil || rows.Anonymous == nil || len(rows.Anonymous) != 0 {
		t.Fatal(rows, err)
	}
	fact := pgcatalog.SharedDependency{Database: 4294967295, Address: ids.ObjectAddress{ClassID: 1259, ObjectID: 4294967295, SubID: 2}, ReferenceClass: 1260, Reference: 4294967295, Kind: 'a'}
	in.SharedDependencies = []pgcatalog.SharedDependency{fact}
	rows, err = core.CaptureCatalogSourceRows(in, produce)
	if err != nil || len(rows.Anonymous) != 1 {
		t.Fatal(rows, err)
	}
	for i, value := range rows.Anonymous[0] {
		if value.Type() != in.Source.Columns[i].Usage.Type {
			t.Fatal("foreign type", i)
		}
	}
	for i, want := range map[int]uint32{0: 4294967295, 1: 1259, 2: 4294967295, 4: 1260, 5: 4294967295} {
		if got, err := rows.Anonymous[0][i].OID(); err != nil || got != want {
			t.Fatal(i, got, err)
		}
	}
	if got, err := rows.Anonymous[0][3].Integer(); err != nil || got != 2 {
		t.Fatal(got, err)
	}
	if got, err := rows.Anonymous[0][6].Text(); err != nil || got != "a" {
		t.Fatal(got, err)
	}
	reject := func(bad pgcatalog.Input) {
		t.Helper()
		rows, err := produce(bad)
		if rows != nil || err == nil {
			t.Fatal("accepted malformed authority", rows, err)
		}
	}
	bad := in
	bad.Owner = nil
	reject(bad)
	bad = in
	bad.Catalog = catalog.Read{}
	reject(bad)
	bad = in
	bad.Roles = authorization.Read{}
	reject(bad)
	bad = in
	bad.Ref.Epoch++
	reject(bad)
	bad = in
	clone := *in.Source
	bad.Source = &clone
	reject(bad)
	bad = in
	bad.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address)
	reject(bad)
	for _, shape := range []string{"width", "name", "type", "nullability"} {
		state := catalog.CloneState(view.State())
		source := *in.Source
		source.Columns = append([]catalog.Column(nil), source.Columns...)
		switch shape {
		case "width":
			source.Columns = source.Columns[:6]
		case "name":
			source.Columns[0].Name = "foreign"
		case "type":
			source.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Int4)
		case "nullability":
			source.Columns[0].Nullable = true
		}
		state.SetDescriptor(source.Address(), &source)
		bad = in
		bad.Source = &source
		bad.Catalog = catalog.NewRead(in.Owner, state)
		reject(bad)
	}
	for _, kind := range []byte{0, 'p', 't', 'n'} {
		bad = in
		edge := fact
		edge.Kind = kind
		bad.SharedDependencies = []pgcatalog.SharedDependency{edge}
		reject(bad)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	bad = in
	bad.Context = canceled
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(rows, err)
	}
	for i, column := range in.Source.Columns {
		if column.Nullable || !column.NullabilityKnown || column.Usage.Type.ID() != []sqltype.ID{26, 26, 26, 23, 26, 26, 18}[i] {
			t.Fatal("shape", i, column)
		}
	}
}

func TestDefaultACLDependencyAddress(t *testing.T) {
	domain := &authorization.Domain{}
	owner, reader := domain.Principal(100), domain.Principal(101)
	record := &catalog.DefaultACLRecord{OID: 98765, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}}}}
	var got []access.RoleDependency
	access.VisitDefaultRoleDependencies(catalog.DefaultACLKey{Creator: owner}, record, func(role authorization.PrincipalRef, dependency access.RoleDependency) bool {
		if dependency.Address != (ids.ObjectAddress{ClassID: 826, ObjectID: 98765}) {
			t.Fatal(dependency)
		}
		if len(got) == 0 && (role != owner || dependency.Kind != 'o') || len(got) == 1 && (role != reader || dependency.Kind != 'a') {
			t.Fatal(role, dependency)
		}
		got = append(got, dependency)
		return true
	})
	if len(got) != 2 {
		t.Fatal(got)
	}
	if !access.VisitDefaultRoleDependencies(catalog.DefaultACLKey{Creator: owner}, record, func(authorization.PrincipalRef, access.RoleDependency) bool { return false }) {
		t.Fatal("missing early exit")
	}
}

func TestRoleDependencyVisitorOwners(t *testing.T) {
	owner := (&authorization.Domain{}).Principal(100)
	descriptors := []catalog.Descriptor{
		&catalog.Database{Owner: owner}, &catalog.ForeignObject{Owner: owner}, &catalog.Extension{Owner: owner}, &catalog.Procedure{Owner: owner}, &catalog.Language{Owner: owner}, &catalog.EventTrigger{Owner: owner}, &catalog.Subscription{Owner: owner}, &catalog.Publication{Owner: owner}, &catalog.Parameter{Owner: owner}, &catalog.Tablespace{Owner: owner}, &catalog.LargeObject{Owner: owner}, &catalog.TSConfiguration{Owner: owner}, &catalog.TSDictionary{Owner: owner}, &catalog.Namespace{Owner: owner}, &catalog.Relation{Owner: owner}, &catalog.Collation{Owner: owner}, &catalog.Conversion{Owner: owner}, &catalog.Statistics{Owner: owner}, &catalog.OperatorFamily{Owner: owner}, &catalog.OperatorClass{Owner: owner}, &catalog.Operator{Owner: owner},
	}
	for _, descriptor := range descriptors {
		count := 0
		access.VisitDescriptorRoleDependencies(descriptor, func(role authorization.PrincipalRef, dependency access.RoleDependency) bool {
			count++
			if role != owner || dependency.Kind != 'o' {
				t.Fatal(descriptor, role, dependency)
			}
			return true
		})
		if count != 1 {
			t.Fatalf("%T owner count %d", descriptor, count)
		}
		if !access.DescriptorRoleDependencies(descriptor, owner, nil) {
			t.Fatalf("%T early exit", descriptor)
		}
	}
}

func TestRoleDependencyVisitorTypeOwners(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	owner := (&authorization.Domain{}).Principal(100)
	scalar, ok := view.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing scalar")
	}
	array, ok := view.LookupType(sqltype.ID(1007))
	if !ok {
		t.Fatal("missing array")
	}
	for _, tc := range []struct {
		descriptor catalog.Descriptor
		count      int
	}{
		{&catalog.Type{SQLType: scalar, Owner: owner}, 1},
		{&catalog.Type{SQLType: array, Owner: owner}, 0},
		{&catalog.Type{SQLType: scalar, Owner: owner, OwningRelation: ids.ObjectAddress{ClassID: 1259, ObjectID: 40000}}, 0},
		{&catalog.Relation{Owner: owner, RelationKind: catalog.IndexRelation}, 0},
		{&catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 40000}, MappedRole: owner}, 1},
	} {
		count := 0
		access.VisitDescriptorRoleDependencies(tc.descriptor, func(ref authorization.PrincipalRef, dependency access.RoleDependency) bool {
			if ref != owner || dependency.Kind != 'o' {
				t.Fatal(ref, dependency)
			}
			count++
			return true
		})
		if count != tc.count {
			t.Fatalf("%T count %d want %d", tc.descriptor, count, tc.count)
		}
	}
}

func TestRoleDependencyVisitorPolicyMultiplicity(t *testing.T) {
	ref := (&authorization.Domain{}).Principal(100)
	address := ids.ObjectAddress{ClassID: 1259, ObjectID: 40000}
	relation := &catalog.Relation{ObjectAddress: address, Security: catalog.RelationSecurity{Policies: []catalog.PolicyDefinition{
		{OID: 40001, Name: "p", Roles: []catalog.PolicyRole{{Ref: ref}, {Ref: ref}}},
		{OID: 40002, Name: "public_policy", Roles: []catalog.PolicyRole{{Public: true}}},
	}}}
	count := 0
	access.VisitDescriptorRoleDependencies(relation, func(role authorization.PrincipalRef, dependency access.RoleDependency) bool {
		if role != ref || dependency.Kind != 'r' || dependency.Address != (ids.ObjectAddress{ClassID: 3256, ObjectID: 40001}) || dependency.PolicyTable != address {
			t.Fatal(role, dependency)
		}
		count++
		return true
	})
	if count != 2 {
		t.Fatal("policy multiplicity", count)
	}
	var collected []access.RoleDependency
	access.DescriptorRoleDependencies(relation, ref, &collected)
	if len(collected) != 2 {
		t.Fatal("diagnostic multiplicity", collected)
	}
}
