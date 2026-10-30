package ddl

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestDefaultTablespaceCreationPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, actor, setting, persistence string
		partitioned, grant, cloned bool
		code, message string
	}{
		{name: "empty", actor: "pg_signal_backend"},
		{name: "missing-provider-default", actor: "pg_signal_backend", setting: "provider_missing_space"},
		{name: "database-default", actor: "pg_signal_backend", setting: "pg_default"},
		{name: "partition-default", actor: "postgres", setting: "pg_default", partitioned: true, code: "0A000", message: "cannot specify default tablespace for partitioned relations"},
		{name: "temporary", actor: "pg_signal_backend", setting: "pg_global", persistence: "temporary", partitioned: true},
		{name: "superuser-global", actor: "postgres", setting: "pg_global", code: "22023", message: "only shared relations can be placed in pg_global tablespace"},
		{name: "ordinary-global", actor: "pg_signal_backend", setting: "pg_global", code: "42501", message: "permission denied for tablespace pg_global"},
		{name: "granted-global", actor: "pg_signal_backend", setting: "pg_global", grant: true, code: "22023", message: "only shared relations can be placed in pg_global tablespace"},
		{name: "cloned-global-skips-acl", actor: "pg_signal_backend", setting: "pg_global", cloned: true, code: "22023", message: "only shared relations can be placed in pg_global tablespace"},
		{name: "cloned-partition-default", actor: "pg_signal_backend", setting: "pg_default", partitioned: true, cloned: true, code: "0A000", message: "cannot specify default tablespace for partitioned relations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			view := f.view(t, tc.actor)
			state := catalog.NewState(1)
			owner := f.owner.Principals.Principal(authorization.BootstrapPrincipalID)
			catalog.InstallBuiltinTablespaces(state, owner)
			state.SetDescriptor(catalog.CurrentDatabaseAddress, f.read.Get(catalog.CurrentDatabaseAddress))
			if tc.grant {
				address := ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: catalog.GlobalTablespaceOID}
				space := *state.Get(address).(*catalog.Tablespace)
				space.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: view.State.CurrentUser}, Privileges: authorization.CreatePrivilege}}}
				state.SetDescriptor(address, &space)
			}
			view.Catalog = catalog.NewRead(f.owner, state)
			view.PairedCatalog = view.Catalog
			var err error
			if tc.cloned {
				err = CheckClonedIndexTablespaceCreation(view.Catalog, tc.setting, tc.persistence, tc.partitioned)
			} else {
				err = CheckDefaultTablespaceCreation(view, authority{}, tc.setting, tc.persistence, tc.partitioned)
			}
			if tc.code == "" {
				if err != nil { t.Fatal(err) }
				return
			}
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
			var got *sqlstate.Error
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("diagnostic got %#v (%v), want %#v", got, err, want)
			}
		})
	}
}
