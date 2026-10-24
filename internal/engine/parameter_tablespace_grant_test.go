package engine

import (
	"context"
	"slices"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/parser"
)

func parameterSpaceRequest(kind string, names ...string) *parser.ObjectGrant {
	r := &parser.ObjectGrant{Kind: kind, Privileges: []parser.GrantPrivilege{{All: true}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "grant_reader"}}}, GrantOption: true}
	for _, name := range names {
		r.Relations = append(r.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: name}}})
	}
	return r
}

func TestObjectGrantParameterPublication(t *testing.T) {
	for _, name := range []string{"lc_messages", "og_custom.value"} {
		t.Run(name, func(t *testing.T) {
			e, op, reader, _ := objectGrantUnitFixture(t)
			before, roles := e.catalog.State, e.roles.Published
			request := parameterSpaceRequest("parameter", name)
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal(err)
			}
			address := e.catalog.State.NameAddress(ids.NameKey{ClassID: ids.ParameterClassID, Name: name})
			d, ok := e.catalog.State.Parameter(address)
			mask := authorization.SetPrivilege | authorization.AlterSystemPrivilege
			if !ok || d == nil || address.ClassID != ids.ParameterClassID || d.Revision() == 0 || d.Owner != e.roles.Domain.Principal(authorization.BootstrapPrincipalID) {
				t.Fatal("parameter did not acquire real catalog authority")
			}
			if before.NameAddress(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) || e.roles.Published.Revision != roles.Revision+1 || !d.ACL.Explicit || len(d.ACL.Entries) != 2 {
				t.Fatal("wrong sparse immutable parameter publication", d.ACL)
			}
			if authorization.ACLRights(d.ACL, d.Owner, []authorization.PrincipalRef{reader}, mask, true) != mask || !slices.Contains(descriptorReferences(d), reader) {
				t.Fatal("parameter ALL/options/dependencies omit sA", d.ACL)
			}
			if err := (role.ParameterProvider{Settings: &e.settings}).Validate(e.catalog.Current(), d); err != nil {
				t.Fatal(err)
			}
			copy := *d
			if err := (role.ParameterProvider{Settings: &e.settings}).Validate(e.catalog.Current(), &copy); err == nil {
				t.Fatal("copied parameter descriptor accepted as authority")
			}
			copy.Owner = authorization.PrincipalRef{}
			if err := access.ValidateACL(e.roles.PublishedRead(&e.catalog.Owner, e.catalog.Current()), &copy); err == nil {
				t.Fatal("parameter zero owner accepted")
			}
			stable, stableRoles := e.catalog.State, e.roles.Published
			if err := applyObjectGrantUnit(e, op, request); err != nil || e.catalog.State != stable || e.roles.Published != stableRoles {
				t.Fatal("repeated parameter grant was not a no-op", err)
			}
			late := parameterSpaceRequest("parameter", name, "work_mem")
			if err := applyObjectGrantUnit(e, op, late); err == nil || e.catalog.State != stable || e.roles.Published != stableRoles {
				t.Fatal("late unsupported parameter published prefix", err)
			}
			request.Revoke, request.GrantOption = true, false
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal(err)
			}
			if _, exists := e.catalog.State.Find(address); exists || e.catalog.State.NameAddress(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) || len(d.ACL.Entries) != 2 {
				t.Fatal("baseline-equivalent parameter row/name not deleted or old ACL mutated")
			}
			stable, stableRoles = e.catalog.State, e.roles.Published
			if err := applyObjectGrantUnit(e, op, request); err != nil || e.catalog.State != stable || e.roles.Published != stableRoles {
				t.Fatal("fresh baseline revoke published a parameter row", err)
			}
		})
	}
}

func TestObjectGrantParameterExplicitEmpty(t *testing.T) {
	e := New()
	owner := e.roles.Domain.Principal(authorization.BootstrapPrincipalID)
	d := &catalog.Parameter{ObjectAddress: ids.ObjectAddress{ClassID: ids.ParameterClassID, ObjectID: 900003}, ObjectRevision: 2, ObjectName: "lc_messages", Owner: owner, ACL: authorization.ACLValue{Explicit: true}}
	candidate := catalog.CloneState(e.catalog.State)
	catalog.InstallParameterGrant(candidate, d)
	if candidate.Get(d.Address()) != d || candidate.NameAddress(catalog.DescriptorNameKey(d)) != d.Address() {
		t.Fatal("explicit empty parameter ACL collapsed to absent")
	}
	n := *d
	n.ACL = authorization.DefaultBaseline(owner, authorization.DefaultParameters, false)
	catalog.InstallParameterGrant(candidate, &n)
	if _, exists := candidate.Find(d.Address()); exists || candidate.NameAddress(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) || !d.ACL.Explicit || len(d.ACL.Entries) != 0 {
		t.Fatal("baseline deletion mutated empty ACL or retained row")
	}
}

func TestObjectGrantTablespacePublication(t *testing.T) {
	for _, builtin := range catalog.BuiltinTablespaces {
		t.Run(builtin.Name, func(t *testing.T) {
			e, op, reader, _ := objectGrantUnitFixture(t)
			address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID}
			original, ok := e.catalog.State.Tablespace(address)
			if !ok || original == nil || original.ACL.Explicit || original.Name() != builtin.Name || original.Owner != e.roles.Domain.Principal(authorization.BootstrapPrincipalID) {
				t.Fatal("missing real builtin tablespace baseline")
			}
			request := parameterSpaceRequest("tablespace", builtin.Name)
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal(err)
			}
			d := e.catalog.State.MustTablespace(address)
			if d == original || original.ACL.Explicit || !d.ACL.Explicit || len(d.ACL.Entries) != 2 || authorization.ACLRights(d.ACL, d.Owner, []authorization.PrincipalRef{reader}, authorization.CreatePrivilege, true) != authorization.CreatePrivilege || !slices.Contains(descriptorReferences(d), reader) {
				t.Fatal("wrong immutable tablespace ALL/options/dependencies", d.ACL)
			}
			if err := (catalog.TablespaceProvider{}).Validate(e.catalog.Current(), d); err != nil {
				t.Fatal(err)
			}
			copy := *d
			if err := (catalog.TablespaceProvider{}).Validate(e.catalog.Current(), &copy); err == nil {
				t.Fatal("copied tablespace descriptor accepted as authority")
			}
			copy.Owner = authorization.PrincipalRef{}
			if err := access.ValidateACL(e.roles.PublishedRead(&e.catalog.Owner, e.catalog.Current()), &copy); err == nil {
				t.Fatal("tablespace zero owner accepted")
			}
			stable, roles := e.catalog.State, e.roles.Published
			if err := applyObjectGrantUnit(e, op, parameterSpaceRequest("tablespace", builtin.Name, "missing")); err == nil || e.catalog.State != stable || e.roles.Published != roles {
				t.Fatal("late missing tablespace published prefix", err)
			}
			request.Revoke, request.GrantOption = true, false
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal(err)
			}
			next := e.catalog.State.MustTablespace(address)
			if !next.ACL.Explicit || !authorization.MatchesDefaultBaseline(next.ACL, next.Owner, authorization.DefaultTablespaces, false) || len(d.ACL.Entries) != 2 {
				t.Fatal("tablespace baseline/revoke snapshot mismatch", next.ACL)
			}
		})
	}
}

func TestObjectGrantTablespaceCapturedACL(t *testing.T) {
	e, op, _, _ := objectGrantUnitFixture(t)
	capture := func(catalog catalog.Read) *exec.Source {
		t.Helper()
		address := catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "pg_tablespace"})
		source := catalog.MustRelation(address)
		if len(source.Columns) != 5 {
			t.Fatal("wrong pg_tablespace column count", len(source.Columns))
		}
		for i, name := range []string{"oid", "spcname", "spcowner", "spcacl", "spcoptions"} {
			if source.Columns[i].Name != name {
				t.Fatal("noncanonical pg_tablespace column order", i, source.Columns[i].Name)
			}
		}
		ref := e.catalog.Domain.Ref(catalog.Epoch(), address)
		e.mu.Lock()
		rows, err := captureCatalogSourceRows(pgcatalog.Input{Context: context.Background(), Catalog: catalog, Roles: e.roles.PublishedRead(&e.catalog.Owner, catalog), Ref: ref, Source: source, Owner: &e.catalog.Owner, Runtime: engineCatalogRuntime{engine: e}}, capturePGTablespace)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	before := e.catalog.Current()
	old := capture(before)
	if len(old.Anonymous) != 2 || len(old.Anonymous[0]) != 5 || !old.Anonymous[0][3].IsNull() || !old.Anonymous[1][3].IsNull() || !old.Anonymous[0][4].IsNull() || !old.Anonymous[1][4].IsNull() {
		t.Fatal("tablespace absent ACL baseline not NULL")
	}
	if err := applyObjectGrantUnit(e, op, parameterSpaceRequest("tablespace", "pg_default")); err != nil {
		t.Fatal(err)
	}
	current := capture(e.catalog.Current())
	if current.Anonymous[0][3].IsNull() || current.Anonymous[0][3].ArrayLen() != 2 || !current.Anonymous[1][3].IsNull() || !old.Anonymous[0][3].IsNull() || !capture(before).Anonymous[0][3].IsNull() || !current.Anonymous[0][4].IsNull() || !current.Anonymous[1][4].IsNull() {
		t.Fatal("captured tablespace ACL root mutated or grant not projected")
	}
}

func TestObjectGrantParameterSettingAccess(t *testing.T) {
	e, ownerOp, _, _ := objectGrantUnitFixture(t)
	readerOp, done := roleOperation(t, e, "grant_reader")
	defer done()
	d := e.settings.Definitions()["lc_messages"]
	if err := settingAccess(d, readerOp); err == nil {
		t.Fatal("unprivileged lc_messages setter admitted")
	}
	request := parameterSpaceRequest("parameter", "lc_messages")
	request.Privileges = []parser.GrantPrivilege{{Name: "alter system"}}
	if err := applyObjectGrantUnit(e, ownerOp, request); err != nil {
		t.Fatal(err)
	}
	if err := settingAccess(d, readerOp); err == nil {
		t.Fatal("ALTER SYSTEM metadata conferred SET")
	}
	request.Privileges = []parser.GrantPrivilege{{Name: "set"}}
	if err := applyObjectGrantUnit(e, ownerOp, request); err != nil {
		t.Fatal(err)
	}
	if err := settingAccess(d, readerOp); err != nil {
		t.Fatal("actual lc_messages setter ignored SET ACL", err)
	}
	if err := settingAccess(e.settings.Definitions()["application_name"], readerOp); err != nil {
		t.Fatal("USERSET unexpectedly depends on ACL", err)
	}
	for _, definition := range []settings.Definition{e.settings.Definitions()["server_encoding"], {Name: "private.protected", Access: settings.Privileged}} {
		if err := settingAccess(definition, readerOp); err == nil {
			t.Fatal("read-only or deny-only custom setter admitted")
		}
	}
	request.Revoke, request.GrantOption = true, false
	if err := applyObjectGrantUnit(e, ownerOp, request); err != nil {
		t.Fatal(err)
	}
	if err := settingAccess(d, readerOp); err == nil {
		t.Fatal("held operation cached old parameter permission")
	}
}
