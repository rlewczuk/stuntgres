package access

import (
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// accessFixture is one engine's catalog owner with a seeded role root, a
// current database owned by the bootstrap superuser, an ordinary schema and
// a TEMP schema, without an engine.
type accessFixture struct {
	owner     *catalog.Owner
	read      catalog.Read
	record    *RoleRecord
	bootstrap authorization.PrincipalRef
	public    *catalog.Namespace
	temporary *catalog.Namespace
}

func newAccessFixture(t *testing.T) accessFixture {
	t.Helper()
	sessions := &ids.Domain{}
	f := accessFixture{owner: &catalog.Owner{Domain: &ids.Domain{}, Principals: &authorization.Domain{}}}
	f.bootstrap = f.owner.Principals.Principal(authorization.BootstrapPrincipalID)
	state := catalog.NewState(1)
	database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: "memory", Owner: f.bootstrap}
	f.public = &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 2200}, ObjectName: "public", ObjectRevision: 1, Owner: f.bootstrap}
	f.temporary = &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 30000}, ObjectName: "pg_temp_1", ObjectRevision: 1, Owner: f.bootstrap, Temporary: sessions.Session(1)}
	for _, d := range []catalog.Descriptor{database, f.public, f.temporary} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	f.read = catalog.NewRead(f.owner, state)
	f.record = &RoleRecord{Root: authorization.Seed(f.owner.Principals), Revision: 1}
	return f
}

// view captures the fixture as current, a seeded role.
func (f accessFixture) view(t *testing.T, current string) View {
	t.Helper()
	roles := RoleRead(f.owner, f.owner.Principals, f.read, f.record)
	actor := roles.LookupRef(current)
	if actor == (authorization.PrincipalRef{}) {
		t.Fatalf("role %s missing", current)
	}
	state := authorization.RoleState{Authenticated: f.bootstrap, SessionUser: f.bootstrap, CurrentUser: actor}
	return View{Owner: f.owner, Catalog: f.read, PairedCatalog: f.read, RoleRecord: f.record, Roles: roles, State: state, SessionRoleState: state, Revision: 1}
}

// countingEnvironment answers the session inputs and counts the calls.
type countingEnvironment struct {
	temporary ids.ObjectAddress
	calls     *int
}

func (e countingEnvironment) TemporaryNamespace() ids.ObjectAddress {
	*e.calls++
	return e.temporary
}
func (e countingEnvironment) ReferencePath() ([]ids.ObjectAddress, error) {
	*e.calls++
	return nil, nil
}

func TestViewRejectsForeignOrStaleObjects(t *testing.T) {
	f := newAccessFixture(t)
	other := newAccessFixture(t)
	v := f.view(t, "postgres")
	env := countingEnvironment{calls: new(int)}
	public := v.Ref(f.public.Address())
	if err := v.CanUse(env, public); err != nil {
		t.Fatalf("own view: %v", err)
	}
	foreignOwner := v
	foreignOwner.Owner = other.owner
	foreignCatalog := v
	foreignCatalog.Catalog, foreignCatalog.PairedCatalog = other.read, other.read
	unpaired := v
	unpaired.PairedCatalog = other.read
	for name, check := range map[string]func() error{
		"foreign owner":     func() error { return foreignOwner.CanUse(env, public) },
		"missing owner":     func() error { _, err := View{}.Object(public); return err },
		"foreign catalog":   func() error { return foreignCatalog.CanUse(env, public) },
		"unpaired catalog":  func() error { return unpaired.CanUse(env, public) },
		"foreign reference": func() error { return v.CanUse(env, other.owner.Domain.Ref(1, f.public.Address())) },
		"stale epoch":       func() error { return v.CanUse(env, f.owner.Domain.Ref(2, f.public.Address())) },
	} {
		if err := check(); err == nil || !strings.Contains(err.Error(), "foreign or stale") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestRoleReadRequiresOwnerDomainAndRoot(t *testing.T) {
	f := newAccessFixture(t)
	other := newAccessFixture(t)
	if _, ok := RoleRead(f.owner, f.owner.Principals, f.read, f.record).Find(f.bootstrap); !ok {
		t.Fatal("own role read lost the bootstrap role")
	}
	for name, read := range map[string]authorization.Read{
		"foreign owner":   RoleRead(other.owner, f.owner.Principals, f.read, f.record),
		"foreign domain":  RoleRead(f.owner, other.owner.Principals, f.read, f.record),
		"missing root":    RoleRead(f.owner, f.owner.Principals, f.read, nil),
		"missing catalog": RoleRead(f.owner, f.owner.Principals, catalog.Read{}, f.record),
	} {
		if read != (authorization.Read{}) {
			t.Errorf("%s: got a non-empty role read", name)
		}
	}
}

func TestViewTemporaryNamespaceOnlyFromEnvironment(t *testing.T) {
	f := newAccessFixture(t)
	v := f.view(t, "pg_monitor")
	calls := 0
	own := countingEnvironment{temporary: f.temporary.Address(), calls: &calls}
	rights, err := v.Effective(own, v.Ref(f.public.Address()), false)
	if err != nil || rights != 0 || calls != 0 {
		t.Fatalf("ordinary schema: rights %v err %v environment calls %d", rights, err, calls)
	}
	rights, err = v.Effective(own, v.Ref(f.temporary.Address()), false)
	if err != nil || rights != authorization.UsagePrivilege|authorization.CreatePrivilege || calls != 1 {
		t.Fatalf("own TEMP schema: rights %v err %v environment calls %d", rights, err, calls)
	}
	elsewhere := countingEnvironment{calls: &calls}
	if rights, err = v.Effective(elsewhere, v.Ref(f.temporary.Address()), false); err != nil || rights != 0 {
		t.Fatalf("other session's TEMP schema: rights %v err %v", rights, err)
	}
}

func TestRowSecurityPrincipalFollowsInnermostDefiner(t *testing.T) {
	domain := &authorization.Domain{}
	current, outer, inner := domain.Principal(100), domain.Principal(101), domain.Principal(102)
	for name, tc := range map[string]struct {
		layers []RowSecurityLayer
		want   authorization.PrincipalRef
	}{
		"direct write":         {nil, current},
		"definer view":         {[]RowSecurityLayer{{Owner: outer}}, outer},
		"invoker view":         {[]RowSecurityLayer{{Owner: outer, Invoker: true}}, current},
		"definer over definer": {[]RowSecurityLayer{{Owner: outer}, {Owner: inner}}, inner},
		"invoker over definer": {[]RowSecurityLayer{{Owner: outer, Invoker: true}, {Owner: inner}}, inner},
	} {
		if got := RowSecurityPrincipal(current, tc.layers); got != tc.want {
			t.Errorf("%s: got %v want %v", name, got, tc.want)
		}
	}
}

func TestOperatorFamilyMemberOwnershipValidation(t *testing.T) {
	f := newAccessFixture(t)
	other := newAccessFixture(t)
	roles := f.view(t, "postgres").Roles
	for _, member := range []catalog.Descriptor{
		&catalog.OperatorFamilyOperator{},
		&catalog.OperatorFamilySupport{},
	} {
		owner, acl, columns := DescriptorACL(member)
		if owner != (authorization.PrincipalRef{}) || acl.Explicit || len(acl.Entries) != 0 || len(columns) != 0 {
			t.Fatal("family member acquired permission state", member)
		}
		if err := ValidateACL(roles, member); err != nil {
			t.Fatal("ownerless family member rejected", member, err)
		}
	}
	family := &catalog.OperatorFamily{Owner: f.bootstrap}
	if err := ValidateACL(roles, family); err != nil {
		t.Fatal("local family owner rejected", err)
	}
	for _, owner := range []authorization.PrincipalRef{{}, other.bootstrap} {
		family.Owner = owner
		if err := ValidateACL(roles, family); err == nil {
			t.Fatal("invalid family owner accepted", owner)
		}
	}
}

func TestAccessMethodOwnerlessACLValidation(t *testing.T) {
	f := newAccessFixture(t)
	other := newAccessFixture(t)
	roles := f.view(t, "postgres").Roles
	method := &catalog.AccessMethod{
		ObjectAddress:  ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 90000},
		ObjectRevision: 1, ObjectName: "ownerless_method",
		Handler: 330, Native: catalog.BtreeAccessMethod,
	}
	owner, acl, columns := DescriptorACL(method)
	if owner != (authorization.PrincipalRef{}) || acl.Explicit || len(acl.Entries) != 0 || len(columns) != 0 {
		t.Fatal("global access method acquired permission state", method)
	}
	if err := ValidateACL(roles, method); err != nil {
		t.Fatal("ownerless access method rejected", err)
	}
	for _, principal := range []authorization.PrincipalRef{f.bootstrap, other.bootstrap} {
		var dependencies []RoleDependency
		if DescriptorRoleDependencies(method, principal, &dependencies) || len(dependencies) != 0 {
			t.Fatal("global access method acquired role dependencies", dependencies)
		}
	}
	// The ownerless classification does not relax any owning descriptor's
	// zero/foreign-principal checks. Structural method validation remains the
	// separate catalog provider's responsibility.
	for _, principal := range []authorization.PrincipalRef{{}, other.bootstrap} {
		if err := ValidateACL(roles, &catalog.OperatorFamily{Owner: principal}); err == nil {
			t.Fatal("owning descriptor accepted invalid principal", principal)
		}
	}
}
