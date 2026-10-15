package engine

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqltype"
)

func TestObjectACLTypeProvenance(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, definition := range []string{
		"CREATE DOMAIN public.acl_provenance_domain AS integer",
		"CREATE TYPE public.acl_provenance_enum AS ENUM ('one')",
		"CREATE TYPE public.acl_provenance_composite AS (a integer)",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, op, definition))
	}
	view := e.CatalogRead()
	for _, name := range []string{"acl_provenance_domain", "acl_provenance_enum", "acl_provenance_composite"} {
		d := domainUnitType(t, view, name)
		for _, address := range []ObjectAddress{d.Address(), {ClassID: TypeClassID, ObjectID: OID(d.array)}} {
			typ := view.state.descriptors[address].(*typeDescriptor)
			acl, err := rawObjectACL(view, address)
			if !typ.aclSyntheticDefault || !defaultDomainACL(typ) || err != nil || acl.Explicit {
				t.Fatal("constructor lost raw absence", name, address, acl, err)
			}
		}
	}
	// Ordinary relation row and associated array constructors also materialize
	// effective USAGE without materializing a raw ACL.
	stage := e.BeginCatalogStage(view)
	e.mu.Lock()
	integer, _ := view.LookupType(sqltype.Int4)
	relation, err := e.newTableDescriptorLocked(ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, "acl_provenance_row", op.RoleState().CurrentUser, []ColumnDescriptor{{Name: "a", Usage: integer.Usage()}}, nil)
	if err == nil {
		err = e.stageRowTypeLocked(stage, relation)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(relation); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view = e.CatalogRead()
	row := view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(relation.rowType)}].(*typeDescriptor)
	for _, address := range []ObjectAddress{row.Address(), {ClassID: TypeClassID, ObjectID: OID(row.array)}} {
		if d := view.state.descriptors[address].(*typeDescriptor); !d.aclSyntheticDefault {
			t.Fatal("relation row/array has explicit raw ACL", address)
		}
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.ACLItemArray} {
		d := view.state.descriptors[ObjectAddress{ClassID: TypeClassID, ObjectID: OID(id)}].(*typeDescriptor)
		if d.aclSyntheticDefault || d.acl.Explicit {
			t.Fatal("builtin raw absence does not need synthetic baseline", id)
		}
	}
	t.Run("extension_constructor", func(t *testing.T) {
		extensionEngine, _, base := intarrayUnitInstalled(t)
		catalog := extensionEngine.CatalogRead()
		for _, address := range []ObjectAddress{base.Address(), {ClassID: TypeClassID, ObjectID: OID(base.array)}} {
			d := catalog.state.descriptors[address].(*typeDescriptor)
			if !d.aclSyntheticDefault || !defaultDomainACL(d) {
				t.Fatal("extension baseline provenance", address)
			}
		}
	})
	t.Run("creation_default_normalization", func(t *testing.T) {
		f := newDefaultUnitFixture(t)
		revoke := defaultUnitStatement("types", "usage", []string{"default_owner"}, nil, []string{"PUBLIC"})
		revoke.Revoke = true
		if err := f.apply(revoke); err != nil {
			t.Fatal(err)
		}
		stage := f.e.BeginCatalogStage(f.e.CatalogRead())
		acl, synthetic := stage.creationTypeACL(f.owner, f.schema)
		if synthetic || !acl.Explicit || len(acl.Entries) != 1 || acl.Entries[0].Grantee.Role != f.owner || acl.Entries[0].Privileges != authorization.UsagePrivilege {
			t.Fatal("custom creation default lost raw ACL", acl, synthetic)
		}
		f.e.DiscardCatalogStage(stage)
		revoke.Revoke = false
		if err := f.apply(revoke); err != nil {
			t.Fatal(err)
		}
		stage = f.e.BeginCatalogStage(f.e.CatalogRead())
		defer f.e.DiscardCatalogStage(stage)
		acl, synthetic = stage.creationTypeACL(f.owner, f.schema)
		if !synthetic || !authorization.MatchesDefaultBaseline(acl, f.owner, authorization.DefaultTypes, false) {
			t.Fatal("baseline-equal creation was not normalized", acl, synthetic)
		}
	})
	d := domainUnitType(t, view, "acl_provenance_domain")
	ownerStage := e.BeginCatalogStage(view)
	target := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	if err := stageOwnership(ownerStage, d, target); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, ownerStage)
	view = e.CatalogRead()
	d = domainUnitType(t, view, "acl_provenance_domain")
	if d.owner != target || !d.aclSyntheticDefault || !defaultDomainACL(d) {
		t.Fatal("owner remap lost raw absence", d)
	}
	other := New()
	remapped, err := other.remapPreparedCatalog(cloneCatalog(view.state), e.roles.root.Read(authorization.PrincipalRef{}), other.roles.root.Read(authorization.PrincipalRef{}), nil)
	if err != nil {
		t.Fatal("principal remap", err)
	}
	imported := remapped.descriptors[d.Address()].(*typeDescriptor)
	if !imported.aclSyntheticDefault || !defaultDomainACL(imported) || imported.owner == d.owner {
		t.Fatal("foreign principal remap lost provenance", imported)
	}
	// Equal effective payloads need not have equal raw provenance. Publication
	// must accept an explicit baseline but serialization must not discard it.
	explicit := *d
	explicit.aclSyntheticDefault = false
	explicit.revision++
	change := e.BeginCatalogStage(view)
	if err := change.Replace(&explicit); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, change)
	acl, err := rawObjectACL(e.CatalogRead(), explicit.Address())
	if err != nil || !reflect.DeepEqual(acl, explicit.acl) || !acl.Explicit || defaultDomainACL(&explicit) {
		t.Fatal("explicit baseline collapsed to NULL", acl, err)
	}
	var output bytes.Buffer
	if err := e.Dump(context.Background(), &output); err == nil || output.Len() != 0 {
		t.Fatal("Dump discarded explicit-baseline provenance", output.String(), err)
	}
	invalid := explicit
	invalid.aclSyntheticDefault = true
	invalid.acl = authorization.ACLValue{Explicit: true}
	if err := (typeProvider{}).Validate(e.CatalogRead(), &invalid); err == nil {
		t.Fatal("accepted synthetic provenance for a nonbaseline ACL")
	}
}
