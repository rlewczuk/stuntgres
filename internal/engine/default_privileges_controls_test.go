package engine

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/control"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"testing"
)

func TestDefaultPrivilegesReplacement(t *testing.T) {
	t.Run("foreign principal remap", func(t *testing.T) {
		source, target := newDefaultUnitFixture(t), newDefaultUnitFixture(t)
		source.grant(t, "tables", "select", nil)
		source.grant(t, "types", "usage", []string{"default_schema"})
		retained := source.e.CatalogRead()
		oldRolesPublication := target.e.RoleStore.Published
		if _, err := target.e.ReplaceCatalog(context.Background(), source.e, retained); err != nil {
			t.Fatal(err)
		}
		global := catalog.DefaultACLKey{Creator: target.owner, Class: authorization.DefaultTables}
		scoped := catalog.DefaultACLKey{Creator: target.owner, Schema: source.schema, Class: authorization.DefaultTypes}
		for _, key := range []catalog.DefaultACLKey{global, scoped} {
			record := target.e.Catalog.State.DefaultACL(key)
			if record == nil {
				t.Fatal("imported default missing", key)
			}
			for _, entry := range record.ACL.Entries {
				if entry.Grantor != target.owner || !entry.Grantee.Public && entry.Grantee.Role != target.owner && entry.Grantee.Role != target.reader {
					t.Fatal("foreign ACL origin not remapped", entry)
				}
			}
		}
		if retained.State().DefaultACL(catalog.DefaultACLKey{Creator: source.owner, Class: authorization.DefaultTables}) == nil || retained.State().DefaultACL(global) != nil || target.e.RoleStore.Published != oldRolesPublication {
			t.Fatal("replacement changed source or target role catalog")
		}
		// Exact target incarnation, not just a recycled name, is fenced by remap.
		candidate := catalog.CloneState(retained.State())
		snapshot := target.e.Roles()
		roles := cloneRoleFixture(target.e.RoleStore.Published)
		replacement := target.reader
		replacement = target.e.RoleStore.Domain.Principal(replacement.ID() + 100)
		retireFixtureRole(roles, target.reader)
		putFixtureRole(roles, authorization.RoleDescriptor{Ref: replacement, Name: "default_reader", Login: true})

		target.e.RoleStore.PublishLocked(roles)
		if _, _, err := target.e.remapPreparedCatalog(candidate, source.e.Roles(), snapshot, nil); err == nil {
			t.Fatal("recycled recipient incarnation imported")
		}
	})
	t.Run("self temporary projection", func(t *testing.T) {
		f := newDefaultUnitFixture(t)
		f.grant(t, "tables", "select", nil)
		roleExecute(t, f.e, f.op, "CREATE TEMP TABLE default_projection_seed(v INT)")
		f.schema = f.op.Session.TemporaryNamespace
		namespace := f.e.Catalog.State.MustNamespace(f.schema)
		assertTemporaryNamespacePair(t, f.e, f.op.Session.SessionToken, f.schema)
		f.grant(t, "tables", "insert", []string{namespace.Name()})
		stage := f.e.BeginCatalogStage(f.e.CatalogRead())
		table := defaultUnitTable(t, f, stage, "temporary_survivor", f.owner)
		if err := defaultUnitPublish(t, f.e, stage); err != nil {
			t.Fatal(err)
		}
		source := f.e.CatalogRead()
		scoped := catalog.DefaultACLKey{Creator: f.owner, Schema: f.schema, Class: authorization.DefaultTables}
		global := catalog.DefaultACLKey{Creator: f.owner, Class: authorization.DefaultTables}
		projection, err := control.PersistentReplacementCatalog(source)
		if err != nil {
			t.Fatal(err)
		}
		if projection.State().DefaultACL(scoped) != nil || projection.State().DefaultACL(global) == nil || source.State().DefaultACL(scoped) == nil {
			t.Fatal("TEMP default projection/source immutability")
		}
		if _, err = f.e.ReplaceCatalog(context.Background(), f.e, source); err != nil {
			t.Fatal(err)
		}
		rights := authorization.ACLRights(table.Security.ACL, f.owner, []authorization.PrincipalRef{f.reader}, authorization.SelectPrivilege|authorization.InsertPrivilege, false)
		if f.e.Catalog.State.Get(table.Address()) != table || f.e.Catalog.State.MustNamespace(table.Namespace()).Temporary != f.op.Session.SessionToken || f.e.Catalog.State.DefaultACL(scoped) != nil || f.e.Catalog.State.DefaultACL(global) == nil || rights&(authorization.SelectPrivilege|authorization.InsertPrivilege) != authorization.SelectPrivilege|authorization.InsertPrivilege {
			t.Fatal("self replacement confused materialized TEMP ACL with directive")
		}
		// Physical end removes directives tied to the retired namespace too.
		grant := defaultUnitStatement("sequences", "usage", []string{"default_owner"}, []string{namespace.Name()}, []string{"default_reader"})
		if err = f.apply(grant); err != nil {
			t.Fatal(err)
		}
		tempKey := catalog.DefaultACLKey{Creator: f.owner, Schema: f.schema, Class: authorization.DefaultSequences}
		endSnapshot := f.e.CatalogRead()
		f.op.Done()
		f.op.Session.RequestEnd(session.CleanClose)
		if err = f.op.Session.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
		assertTemporaryNamespacePair(t, f.e, f.op.Session.SessionToken, f.schema)
		if f.e.Catalog.State.Get(table.Address()) != nil {
			t.Fatal("physical end retained TEMP child")
		}
		if f.e.Catalog.State.DefaultACL(tempKey) != nil || endSnapshot.State().DefaultACL(tempKey) == nil || f.e.Catalog.State.DefaultACL(global) == nil {
			t.Fatal("TEMP retirement leaked scoped defaults or changed retained source")
		}
	})
	t.Run("foreign TEMP filtering and namespace collision", func(t *testing.T) {
		source, target := newDefaultUnitFixture(t), newDefaultUnitFixture(t)
		source.grant(t, "tables", "select", []string{"default_schema"})
		source.grant(t, "sequences", "usage", nil)
		// Target has a live TEMP namespace at exactly the imported schema identity.
		state := catalog.CloneState(target.e.Catalog.State)
		ns := *state.MustNamespace(target.schema)
		ns.ObjectName = "pg_temp_collision"
		ns.Temporary = target.op.Session.SessionToken
		state.UnbindName(catalog.DescriptorNameKey(state.Get(target.schema)))
		state.SetDescriptor(target.schema, &ns)
		state.BindName(catalog.DescriptorNameKey(&ns), target.schema)
		target.e.Catalog.PublishLocked(state)
		target.op.Session.TemporaryNamespace = target.schema
		if _, err := target.e.ReplaceCatalog(context.Background(), source.e, source.e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		imported := target.e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "default_schema"})
		if imported == target.schema || imported == (ids.ObjectAddress{}) || target.e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: target.owner, Schema: imported, Class: authorization.DefaultTables}) == nil || target.e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: target.owner, Schema: target.schema, Class: authorization.DefaultTables}) != nil {
			t.Fatal("namespace collision did not remap imported policy identity")
		}
		if target.e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: target.owner, Class: authorization.DefaultSequences}) == nil {
			t.Fatal("foreign global policy lost")
		}
		// A source TEMP-only record is projected out even on a foreign import.
		tempSource := catalog.CloneState(source.e.Catalog.State)
		tempNS := *tempSource.MustNamespace(source.schema)
		tempNS.ObjectName = "pg_temp_source"
		tempNS.Temporary = source.op.Session.SessionToken
		tempSource.UnbindName(catalog.DescriptorNameKey(tempSource.Get(source.schema)))
		tempSource.SetDescriptor(source.schema, &tempNS)
		tempSource.BindName(catalog.DescriptorNameKey(&tempNS), source.schema)
		fresh := newDefaultUnitFixture(t)
		if _, err := fresh.e.ReplaceCatalog(context.Background(), source.e, source.e.Catalog.Read(tempSource)); err != nil {
			t.Fatal(err)
		}
		if fresh.e.Catalog.State.DefaultACLCount() != 1 || fresh.e.Catalog.State.DefaultACL(catalog.DefaultACLKey{Creator: fresh.owner, Class: authorization.DefaultSequences}) == nil || tempSource.DefaultACL(catalog.DefaultACLKey{Creator: source.owner, Schema: source.schema, Class: authorization.DefaultTables}) == nil {
			t.Fatal("foreign TEMP policy/source filtering")
		}
	})
}

func TestDefaultPrivilegesControls(t *testing.T) {
	for _, class := range []string{"tables", "sequences", "functions", "types", "schemas", "large objects"} {
		t.Run(class, func(t *testing.T) {
			f := newDefaultUnitFixture(t)
			f.grant(t, class, "all", nil)
			var output bytes.Buffer
			err := Dump(context.Background(), f.e, &output)
			if err == nil || !strings.Contains(err.Error(), "default privileges") || output.Len() != 0 {
				t.Fatal("default-only Dump wrote/lost policy", err, output.String())
			}
		})
	}
	f := newDefaultUnitFixture(t)
	empty := defaultUnitStatement("large objects", "all", []string{"default_owner"}, nil, []string{"default_owner"})
	empty.Revoke = true
	if err := f.apply(empty); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Dump(context.Background(), f.e, &output); err == nil || output.Len() != 0 {
		t.Fatal("stored-empty Dump", err)
	}
	before := f.e.CatalogRead()
	rolesPublication := f.e.RoleStore.Published
	roles := f.e.Roles()
	edges := roles.Memberships()
	err := Restore(context.Background(), f.e, strings.NewReader("CREATE TABLE before_reject(x INT); ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC;"))
	if err == nil || f.e.Catalog.State != before.State() || f.e.RoleStore.Published != rolesPublication {
		t.Fatal("Restore admitted defaults or changed original", err)
	}
	if err = Reset(context.Background(), f.e); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACLCount() != 0 || f.e.RoleStore.Published != rolesPublication || !reflect.DeepEqual(f.e.Roles().Memberships(), edges) {
		t.Fatal("Reset default/role/edge boundary")
	}
	// Pristine dependent hardwired TYPE baselines must not make ordinary table
	// dump/restore fail, nor require a broadened generic TypeObject fallback.
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	f.schema = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	f.owner = f.e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	table := defaultUnitTable(t, f, stage, "pristine", f.owner)
	if err = defaultUnitPublish(t, f.e, stage); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = Dump(context.Background(), f.e, &output); err != nil {
		t.Fatal("pristine dependent baseline Dump", err)
	}
	target := New()
	if err = Restore(context.Background(), target, bytes.NewReader(output.Bytes())); err != nil {
		t.Fatal("pristine dependent baseline Restore", err)
	}
	if len(output.Bytes()) == 0 || table.RowType == 0 {
		t.Fatal("pristine control witness missing")
	}
}
