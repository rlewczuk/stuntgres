package schemacmds_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

// Exercise genuine private identity staging and publication, not SQL execution.
// Maintenance-only database facts must not become a new DDL authority boundary.
func TestTypeLifecycleDatabaseMaintenanceAuthority(t *testing.T) {
	for _, moving := range []bool{false, true} {
		for _, change := range []string{"horizon", "owner", "acl", "identity"} {
			t.Run(fmt.Sprintf("moving=%t/%s", moving, change), func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				enginetest.RoleExecute(t, e, op, "CREATE TYPE public.mood AS ENUM ('a')")
				enginetest.RoleExecute(t, e, op, "CREATE SCHEMA moved")
				table := domainUnitTable(t, e, op, "CREATE TABLE public.holder(v public.mood)")
				before := e.CatalogRead()
				old := domainUnitType(t, before, "mood")
				oldArray := before.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)})
				oldDatabase := before.MustDatabase(e.Catalog.DatabaseAddress())
				value, err := old.SQLType.Input(context.Background(), "a", -1)
				if err != nil {
					t.Fatal(err)
				}
				original := &storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}}}}
				e.Mu.Lock()
				e.Storage.InstallRelationLocked(table.Address(), original)
				e.Visibility.SetCommitSeqLocked(1)
				e.Mu.Unlock()
				q := "ALTER TYPE public.mood RENAME TO renamed"
				name, arrayName, namespace := "renamed", "_renamed", old.Namespace()
				if moving {
					q, name, arrayName = "ALTER TYPE public.mood SET SCHEMA moved", "mood", oldArray.Name()
					var ok bool
					namespace, ok = before.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "moved"})
					if !ok {
						t.Fatal("missing genuine destination")
					}
				}
				stage := domainUnitStage(t, e, op, q)
				defer e.DiscardCatalogStage(stage)
				if stage.DatabasePredecessor != oldDatabase || stage.DatabaseAuthority != oldDatabase {
					t.Fatal("identity stage did not capture genuine database authority")
				}
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if change == "horizon" {
					e.Mu.Lock()
					e.DatabaseFrozenXID = oldDatabase.FrozenXID + 1
					core.EnginePublishDatabaseFrozenXIDLocked(e)
					e.Mu.Unlock()
					expected := *oldDatabase
					expected.FrozenXID++
					if !reflect.DeepEqual(e.CatalogRead().MustDatabase(oldDatabase.Address()), &expected) {
						t.Fatal("maintenance publisher changed more than the horizon")
					}
				} else {
					database := *oldDatabase
					database.ObjectRevision++
					switch change {
					case "owner":
						database.Owner = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_read_all_data")
						if database.Owner == (authorization.PrincipalRef{}) || database.Owner == oldDatabase.Owner {
							t.Fatal("missing distinct genuine owner")
						}
					case "acl":
						database.ACL = authorization.ACLValue{Explicit: true}
					case "identity":
						database.ObjectName += "_replacement"
					}
					replacement := e.BeginCatalogStage(e.CatalogRead())
					defer e.DiscardCatalogStage(replacement)
					if err := replacement.Replace(&database); err != nil {
						t.Fatal(err)
					}
					domainUnitPublish(t, e, replacement)
				}
				intervening := e.CatalogRead()
				_, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
				if change != "horizon" {
					if !errors.Is(err, ids.ErrCatalogReplaced) || e.CatalogRead().State() != intervening.State() || e.Storage.Relations[table.Address()] != original {
						t.Fatal("genuine database authority change did not veto identity publication atomically", err)
					}
					if intervening.MustType(old.Address()) != old || intervening.MustType(oldArray.Address()) != oldArray {
						t.Fatal("veto changed source identities")
					}
					return
				}
				if err != nil {
					t.Fatal("maintenance-only horizon update vetoed genuine type identity publication", err)
				}
				current := e.CatalogRead()
				next, array := current.MustType(old.Address()), current.MustType(oldArray.Address())
				if next.Name() != name || next.Namespace() != namespace || next.Array != old.Array || next.Owner != old.Owner || array.Name() != arrayName || array.Namespace() != namespace || array.SQLType.Element() != next.SQLType {
					t.Fatal("identity or reciprocal companion lost")
				}
				if current.NameAddress(catalog.DescriptorNameKey(old)) != (ids.ObjectAddress{}) || current.NameAddress(catalog.DescriptorNameKey(next)) != old.Address() {
					t.Fatal("identity lookup cutover lost")
				}
				store := e.Storage.Relations[table.Address()]
				if len(store.Versions) != 1 || store.Versions[0].Row != 1 || store.Versions[0].Version != 1 || store.Versions[0].Born != 1 || store.Versions[0].Values[0].Type() != next.SQLType || current.MustRelation(table.Address()).Columns[0].Usage.Type != next.SQLType {
					t.Fatal("stored identity/data generation closure lost")
				}
				text, err := next.SQLType.Output(context.Background(), store.Versions[0].Values[0])
				if err != nil || text != "a" || current.MustDatabase(oldDatabase.Address()) != intervening.MustDatabase(oldDatabase.Address()) || e.DatabaseFrozenXID != oldDatabase.FrozenXID+1 {
					t.Fatal("identity publication lost data or the new maintenance horizon", text, err)
				}
				if before.MustType(old.Address()) != old || old.Name() != "mood" || oldArray.SQLType.Element() != old.SQLType || original.Versions[0].Values[0].Type() != old.SQLType || before.MustDatabase(oldDatabase.Address()) != oldDatabase {
					t.Fatal("retained predecessor mutated")
				}
			})
		}
	}
}

func TestTypeLifecycleDatabaseAuthorityMetadata(t *testing.T) {
	e := engine.New()
	original := e.CatalogRead().MustDatabase(e.Catalog.DatabaseAddress())
	foreign := engine.New()
	foreignOwner := foreign.CatalogRead().MustDatabase(foreign.Catalog.DatabaseAddress()).Owner
	otherOwner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_read_all_data")
	for _, tc := range []struct {
		name   string
		change func(*catalog.Database)
	}{
		{"address", func(d *catalog.Database) { d.ObjectAddress.ObjectID++ }},
		{"revision", func(d *catalog.Database) { d.ObjectRevision++ }},
		{"name", func(d *catalog.Database) { d.ObjectName += "_other" }},
		{"owner", func(d *catalog.Database) { d.Owner = otherOwner }},
		{"owner_domain", func(d *catalog.Database) { d.Owner = foreignOwner }},
		{"acl_explicit", func(d *catalog.Database) { d.ACL.Explicit = !d.ACL.Explicit }},
		{"acl_entries", func(d *catalog.Database) {
			d.ACL.Entries = []authorization.ACLEntry{{Grantor: original.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ConnectPrivilege}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := *original
			tc.change(&changed)
			if catalog.DatabaseAuthorityCompatible(&changed, original) || catalog.DatabaseAuthorityCompatible(original, &changed) {
				t.Fatal("non-maintenance database drift accepted", tc.name)
			}
		})
	}
	horizon := *original
	horizon.FrozenXID++
	if !catalog.DatabaseAuthorityCompatible(original, original) || !catalog.DatabaseAuthorityCompatible(&horizon, original) || !catalog.DatabaseAuthorityCompatible(original, &horizon) {
		t.Fatal("identical authority rejected for pointer or horizon-only change")
	}
	if catalog.DatabaseAuthorityCompatible(&catalog.Namespace{}, original) || catalog.DatabaseAuthorityCompatible(nil, original) || catalog.DatabaseAuthorityCompatible(original, nil) {
		t.Fatal("foreign descriptor kind or missing database accepted")
	}
}
