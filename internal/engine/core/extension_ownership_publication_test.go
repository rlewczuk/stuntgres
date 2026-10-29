package core_test

import (
	"context"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestExtensionOwnershipPublication(t *testing.T) {
	for _, kind := range []string{"destination_acl", "role_membership", "destination_reincarnation"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			admin, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{`CREATE ROLE eo_unit_owner SUPERUSER`, `CREATE ROLE eo_unit_actor LOGIN INHERIT`, `GRANT eo_unit_owner TO eo_unit_actor`, `CREATE SCHEMA eo_unit_old`, `CREATE SCHEMA eo_unit_new`, `GRANT CREATE ON SCHEMA eo_unit_new TO eo_unit_owner`} {
				enginetest.RoleExecute(t, e, admin, q)
			}
			// A real superuser installation owns its own function members. Demote
			// that principal before relocation so inherited ordinary ownership, not
			// superuser authority or bootstrap-owned unaccent, reaches validation.
			grant := extensionUnitGrant(e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress).Name(), "eo_unit_owner")
			if err := extensionUnitExecute(t, e, admin, grant); err != nil {
				t.Fatal(err)
			}
			actor, actorDone := enginetest.RoleOperation(t, e, "eo_unit_actor")
			defer actorDone()
			enginetest.RoleExecute(t, e, actor, `SET ROLE eo_unit_owner`)
			enginetest.RoleExecute(t, e, actor, `CREATE EXTENSION fuzzystrmatch SCHEMA eo_unit_old`)
			enginetest.RoleExecute(t, e, actor, `RESET ROLE`)
			enginetest.RoleExecute(t, e, admin, `ALTER ROLE eo_unit_owner NOSUPERUSER`)
			base := e.CatalogRead()
			ext := base.MustExtension(base.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "fuzzystrmatch"}))
			destination := base.MustNamespace(base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "eo_unit_new"}))
			provider := e.Catalog.Providers[ids.ExtensionClassID]
			var fired atomic.Bool
			e.Catalog.Providers[ids.ExtensionClassID] = alterDropProvider{Provider: provider, callback: func() {
				if !fired.CompareAndSwap(false, true) {
					return
				}
				switch kind {
				case "destination_acl":
					ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), destination.Address())
					if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
						t.Error(err)
					}
				case "role_membership":
					enginetest.RoleExecute(t, e, admin, `REVOKE eo_unit_owner FROM eo_unit_actor`)
				case "destination_reincarnation":
					// Test the lower publication fence with a real successor identity,
					// independently of the SQL lock manager's admission ordering.
					e.Mu.Lock()
					state := catalog.CloneState(e.Catalog.State)
					state.UnbindName(catalog.DescriptorNameKey(destination))
					state.RemoveDescriptor(destination.Address())
					next := *destination
					next.ObjectAddress.ObjectID += 1000000
					state.SetDescriptor(next.Address(), &next)
					state.BindName(catalog.DescriptorNameKey(&next), next.Address())
					e.Catalog.PublishLocked(state)
					e.Mu.Unlock()
				}
			}}
			defer func() { e.Catalog.Providers[ids.ExtensionClassID] = provider }()
			err := extensionUnitExecute(t, e, actor, enginetest.Statement(t, `ALTER EXTENSION fuzzystrmatch SET SCHEMA eo_unit_new`))
			if !fired.Load() {
				t.Fatal("utility never reached outside-lock provider validation")
			}
			if err == nil {
				t.Fatal("stale authority published relocation")
			}
			current := e.CatalogRead()
			if current.Get(ext.Address()) != ext {
				t.Fatal("rejected publication replaced extension")
			}
			for _, a := range ext.Members {
				if current.Get(a) != base.Get(a) {
					t.Fatal("rejected publication replaced member", a)
				}
			}
		})
	}
}
