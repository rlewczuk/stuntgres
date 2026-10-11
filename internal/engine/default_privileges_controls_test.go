package engine

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultPrivilegesReplacement(t *testing.T) {
	t.Run("foreign principal remap", func(t *testing.T) {
		source, target := newDefaultUnitFixture(t), newDefaultUnitFixture(t)
		source.grant(t, "tables", "select", nil)
		source.grant(t, "types", "usage", []string{"default_schema"})
		retained := source.e.CatalogRead()
		oldRoles := target.e.Roles()
		if _, err := target.e.ReplaceCatalog(context.Background(), retained); err != nil {
			t.Fatal(err)
		}
		global := defaultACLKey{creator: target.owner, class: defaultTables}
		scoped := defaultACLKey{creator: target.owner, schema: source.schema, class: defaultTypes}
		for _, key := range []defaultACLKey{global, scoped} {
			record := target.e.state.defaultACLs[key]
			if record == nil {
				t.Fatal("imported default missing", key)
			}
			for _, entry := range record.acl.Entries {
				if entry.Grantor != target.owner || !entry.Grantee.Public && entry.Grantee.Role != target.owner && entry.Grantee.Role != target.reader {
					t.Fatal("foreign ACL origin not remapped", entry)
				}
			}
		}
		if retained.state.defaultACLs[defaultACLKey{creator: source.owner, class: defaultTables}] == nil || retained.state.defaultACLs[global] != nil || target.e.Roles().state != oldRoles.state {
			t.Fatal("replacement changed source or target role catalog")
		}
		// Exact target incarnation, not just a recycled name, is fenced by remap.
		candidate := cloneCatalog(retained.state)
		snapshot := target.e.Roles()
		roles := cloneRoles(target.e.roles)
		replacement := target.reader
		replacement.id += 100
		delete(roles.roles, target.reader)
		roles.roles[replacement] = RoleDescriptor{Ref: replacement, Name: "default_reader", Login: true}
		roles.names["default_reader"] = replacement
		target.e.roles = roles
		if _, err := target.e.remapPreparedCatalog(candidate, source.e.Roles(), snapshot, nil); err == nil {
			t.Fatal("recycled recipient incarnation imported")
		}
	})
	t.Run("self temporary projection", func(t *testing.T) {
		f := newDefaultUnitFixture(t)
		f.grant(t, "tables", "select", nil)
		f.grant(t, "tables", "insert", []string{"default_schema"})
		state := cloneCatalog(f.e.state)
		namespace := *state.descriptors[f.schema].(*namespaceDescriptor)
		namespace.name = "pg_temp_default_unit"
		namespace.temporary = f.op.session.token
		delete(state.names, descriptorNameKey(state.descriptors[f.schema]))
		state.descriptors[f.schema] = &namespace
		state.names[descriptorNameKey(&namespace)] = f.schema
		f.e.state = state
		f.op.session.temporaryNamespace = f.schema
		stage := f.e.BeginCatalogStage(f.e.CatalogRead())
		table := defaultUnitTable(t, f, stage, "temporary_survivor", f.owner)
		if err := defaultUnitPublish(t, f.e, stage); err != nil {
			t.Fatal(err)
		}
		source := f.e.CatalogRead()
		scoped := defaultACLKey{creator: f.owner, schema: f.schema, class: defaultTables}
		global := defaultACLKey{creator: f.owner, class: defaultTables}
		projection, err := persistentReplacementCatalog(source)
		if err != nil {
			t.Fatal(err)
		}
		if projection.state.defaultACLs[scoped] != nil || projection.state.defaultACLs[global] == nil || source.state.defaultACLs[scoped] == nil {
			t.Fatal("TEMP default projection/source immutability")
		}
		if _, err = f.e.ReplaceCatalog(context.Background(), source); err != nil {
			t.Fatal(err)
		}
		rights := aclRights(table.acl, f.owner, []PrincipalRef{f.reader}, SelectPrivilege|InsertPrivilege, false)
		if f.e.state.descriptors[table.address] != table || f.e.state.descriptors[table.namespace].(*namespaceDescriptor).temporary != f.op.session.token || f.e.state.defaultACLs[scoped] != nil || f.e.state.defaultACLs[global] == nil || rights&(SelectPrivilege|InsertPrivilege) != SelectPrivilege|InsertPrivilege {
			t.Fatal("self replacement confused materialized TEMP ACL with directive")
		}
		// Physical end removes directives tied to the retired namespace too.
		grant := defaultUnitStatement("sequences", "usage", []string{"default_owner"}, []string{"pg_temp_default_unit"}, []string{"default_reader"})
		if err = f.apply(grant); err != nil {
			t.Fatal(err)
		}
		tempKey := defaultACLKey{creator: f.owner, schema: f.schema, class: defaultSequences}
		endSnapshot := f.e.CatalogRead()
		f.op.Done()
		f.op.session.RequestEnd(CleanClose)
		if err = f.op.session.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
		if f.e.state.defaultACLs[tempKey] != nil || endSnapshot.state.defaultACLs[tempKey] == nil || f.e.state.defaultACLs[global] == nil {
			t.Fatal("TEMP retirement leaked scoped defaults or changed retained source")
		}
	})
	t.Run("foreign TEMP filtering and namespace collision", func(t *testing.T) {
		source, target := newDefaultUnitFixture(t), newDefaultUnitFixture(t)
		source.grant(t, "tables", "select", []string{"default_schema"})
		source.grant(t, "sequences", "usage", nil)
		// Target has a live TEMP namespace at exactly the imported schema identity.
		state := cloneCatalog(target.e.state)
		ns := *state.descriptors[target.schema].(*namespaceDescriptor)
		ns.name = "pg_temp_collision"
		ns.temporary = target.op.session.token
		delete(state.names, descriptorNameKey(state.descriptors[target.schema]))
		state.descriptors[target.schema] = &ns
		state.names[descriptorNameKey(&ns)] = target.schema
		target.e.state = state
		target.op.session.temporaryNamespace = target.schema
		if _, err := target.e.ReplaceCatalog(context.Background(), source.e.CatalogRead()); err != nil {
			t.Fatal(err)
		}
		imported := target.e.state.names[NameKey{ClassID: NamespaceClassID, Name: "default_schema"}]
		if imported == target.schema || imported == (ObjectAddress{}) || target.e.state.defaultACLs[defaultACLKey{creator: target.owner, schema: imported, class: defaultTables}] == nil || target.e.state.defaultACLs[defaultACLKey{creator: target.owner, schema: target.schema, class: defaultTables}] != nil {
			t.Fatal("namespace collision did not remap imported policy identity")
		}
		if target.e.state.defaultACLs[defaultACLKey{creator: target.owner, class: defaultSequences}] == nil {
			t.Fatal("foreign global policy lost")
		}
		// A source TEMP-only record is projected out even on a foreign import.
		tempSource := cloneCatalog(source.e.state)
		tempNS := *tempSource.descriptors[source.schema].(*namespaceDescriptor)
		tempNS.name = "pg_temp_source"
		tempNS.temporary = source.op.session.token
		delete(tempSource.names, descriptorNameKey(tempSource.descriptors[source.schema]))
		tempSource.descriptors[source.schema] = &tempNS
		tempSource.names[descriptorNameKey(&tempNS)] = source.schema
		fresh := newDefaultUnitFixture(t)
		if _, err := fresh.e.ReplaceCatalog(context.Background(), CatalogRead{source.e, tempSource}); err != nil {
			t.Fatal(err)
		}
		if len(fresh.e.state.defaultACLs) != 1 || fresh.e.state.defaultACLs[defaultACLKey{creator: fresh.owner, class: defaultSequences}] == nil || tempSource.defaultACLs[defaultACLKey{creator: source.owner, schema: source.schema, class: defaultTables}] == nil {
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
			err := f.e.Dump(context.Background(), &output)
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
	if err := f.e.Dump(context.Background(), &output); err == nil || output.Len() != 0 {
		t.Fatal("stored-empty Dump", err)
	}
	before := f.e.CatalogRead()
	roles := f.e.Roles()
	edges := roles.Memberships()
	err := f.e.Restore(context.Background(), strings.NewReader("CREATE TABLE before_reject(x INT); ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO PUBLIC;"))
	if err == nil || f.e.state != before.state || f.e.Roles().state != roles.state {
		t.Fatal("Restore admitted defaults or changed original", err)
	}
	if err = f.e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.e.state.defaultACLs) != 0 || f.e.roles != roles.state || !reflect.DeepEqual(f.e.Roles().Memberships(), edges) {
		t.Fatal("Reset default/role/edge boundary")
	}
	// Pristine dependent hardwired TYPE baselines must not make ordinary table
	// dump/restore fail, nor require a broadened generic TypeObject fallback.
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	f.schema = ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	f.owner = f.e.roles.names["postgres"]
	table := defaultUnitTable(t, f, stage, "pristine", f.owner)
	if err = defaultUnitPublish(t, f.e, stage); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err = f.e.Dump(context.Background(), &output); err != nil {
		t.Fatal("pristine dependent baseline Dump", err)
	}
	target := New()
	if err = target.Restore(context.Background(), bytes.NewReader(output.Bytes())); err != nil {
		t.Fatal("pristine dependent baseline Restore", err)
	}
	if len(output.Bytes()) == 0 || table.rowType == 0 {
		t.Fatal("pristine control witness missing")
	}
}
