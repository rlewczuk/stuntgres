package access

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestPublicationMappingOwnerlessACL(t *testing.T) {
	f, other := newAccessFixture(t), newAccessFixture(t)
	roles := f.view(t, "postgres").Roles
	publication := &catalog.Publication{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationClassID, ObjectID: 90001}, ObjectRevision: 1, ObjectName: "publication_acl", Owner: f.bootstrap, GeneratedColumns: 'n'}
	mappings := []catalog.Descriptor{
		&catalog.PublicationRelation{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationRelationClassID, ObjectID: 90002}, ObjectRevision: 1, Publication: publication.Address(), Relation: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 90003}},
		&catalog.PublicationNamespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.PublicationNamespaceClassID, ObjectID: 90004}, ObjectRevision: 1, Publication: publication.Address(), Schema: f.public.Address()},
	}
	for _, mapping := range mappings {
		owner, acl, columns := DescriptorACL(mapping)
		if owner != (authorization.PrincipalRef{}) || acl.Explicit || len(acl.Entries) != 0 || len(columns) != 0 || ClassPrivileges(mapping) != 0 {
			t.Fatal("publication mapping acquired independent ACL authority", mapping)
		}
		if err := ValidateACL(roles, mapping); err != nil {
			t.Fatal("ownerless mapping rejected before endpoint provider validation", err)
		}
		for _, principal := range []authorization.PrincipalRef{f.bootstrap, other.bootstrap} {
			var dependencies []RoleDependency
			if DescriptorRoleDependencies(mapping, principal, &dependencies) || len(dependencies) != 0 {
				t.Fatal("publication mapping acquired independent role ownership", dependencies)
			}
		}
	}
	if err := ValidateACL(roles, publication); err != nil {
		t.Fatal("actual publication owner rejected", err)
	}
	var dependencies []RoleDependency
	DescriptorRoleDependencies(publication, f.bootstrap, &dependencies)
	if !DescriptorRoleDependencies(publication, f.bootstrap, nil) || len(dependencies) != 1 || dependencies[0].Address != publication.Address() || dependencies[0].Kind != 'o' {
		t.Fatal("publication owner dependency lost", dependencies)
	}
	for _, principal := range []authorization.PrincipalRef{{}, other.bootstrap} {
		invalid := *publication
		invalid.Owner = principal
		if err := ValidateACL(roles, &invalid); err == nil {
			t.Fatal("owning publication accepted a foreign/zero principal")
		}
	}
}

func TestPublicationDatabaseCreateAuthority(t *testing.T) {
	f := newAccessFixture(t)
	database := *f.read.MustDatabase(f.owner.DatabaseAddress())
	database.ObjectName = `publication "odd"/db`
	state := catalog.CloneState(f.read.State())
	state.SetDescriptor(database.Address(), &database)
	f.read = f.read.WithState(state)
	calls := 0
	env := countingEnvironment{calls: &calls}
	denied := f.view(t, "pg_monitor")
	baseline, ok := OwnershipDatabaseCreate(denied, env).(*sqlstate.Error)
	if !ok || baseline.Code != "42501" {
		t.Fatal("fixture did not reach normal CREATE denial", baseline)
	}
	diagnostic, ok := PublicationDatabaseCreate(denied, env).(*sqlstate.Error)
	if !ok || diagnostic.Code != baseline.Code || diagnostic.Severity != baseline.Severity || diagnostic.Detail != baseline.Detail || diagnostic.Message != "permission denied for database "+database.Name() {
		t.Fatal("publication diagnostic lost genuine database authority", diagnostic)
	}
	if err := PublicationDatabaseCreate(f.view(t, "postgres"), env); err != nil {
		t.Fatal("actual database owner denied", err)
	}
	if calls != 0 {
		t.Fatal("database CREATE consulted unrelated namespace environment", calls)
	}
}
