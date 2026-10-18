package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// ObjectGrant remains an immediate lock-safe role utility. This exercises its
// real retained-message candidate and outer publication, not a mock ACL stage.
func TestRoleOwnershipObjectGrantMessageDatabaseFence(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, outcome := range []string{"database", "cancel", "healthy"} {
			name := map[bool]string{false: "committed", true: "private"}[private] + "/" + outcome
			t.Run(name, func(t *testing.T) {
				// Reuse genuine typed rows, constraints and index roots as immutable
				// witnesses. Only ObjectGrant changes in this tested message.
				e, session, root, committed, changes := ownershipPartitionConstraintFixture(t, private)
				bootstrap, done := roleOperation(t, e, "postgres")
				reader := roleAttributeCreate(t, e, bootstrap, "object_fence_reader", false, parser.RoleOptions{}).Ref
				done()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				message := batchUnitStart(t, session, ctx)
				defer message.end(context.Canceled)
				if private {
					session.transactions.catalog = changes
				}
				auth, err := message.op.Authorization()
				if err != nil {
					t.Fatal(err)
				}
				actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
				if err != nil || actor.Superuser {
					t.Fatal("fixture is not an ordinary actor", err)
				}
				dbRole := auth.roles.LookupRef("pg_database_owner")
				table := auth.catalog.MustRelation(root.Address)
				if table.owner != dbRole || table.owner == actor.Ref || auth.databasePredecessor != committed || auth.pairedCatalog != auth.catalog {
					t.Fatal("fixture lacks canonical inherited DB-owner authority")
				}
				if private && auth.catalog.Get(currentDatabaseAddress) == committed {
					t.Fatal("private DB authority is not distinct from committed predecessor")
				}
				if err := auth.Check(AccessRequirement{Object: root, OwnerOnly: true}); err != nil {
					t.Fatal("selected DB owner cannot authorize actual table", err)
				}
				checkReader := func(view AuthorizationView, allowed bool) {
					t.Helper()
					view.state.CurrentUser = reader
					err := view.Check(AccessRequirement{Object: root, Privileges: authorization.SelectPrivilege})
					if allowed {
						if err != nil {
							t.Fatal("actual staged reader right is absent", err)
						}
					} else {
						var diagnostic *sqlstate.Error
						if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" {
							t.Fatal("reader acquired an unpublished SELECT right", err)
						}
					}
				}
				checkReader(auth, false)
				oldRoles, oldCatalog := e.roles, e.catalog.state
				sessionRevision, epoch := session.authorization.revision, oldCatalog.epoch
				oldACL := authorization.ACLValue{Explicit: table.acl.Explicit, Entries: append([]authorization.ACLEntry(nil), table.acl.Entries...)}
				oldKeys := append([]uniqueConstraint(nil), table.uniqueConstraints...)
				before := indexMaintenanceCapture(e)
				leaf := roleTableRef(t, e, "leaf").Address
				rows := append([]rowVersion(nil), e.storage[leaf].versions...)
				for i := range rows {
					rows[i].values = append([]sqltype.Value(nil), rows[i].values...)
				}
				if len(rows) != 1 || len(oldKeys) != 1 {
					t.Fatal("fixture lacks real data and constraint witnesses")
				}
				request := &parser.ObjectGrant{Privileges: []parser.GrantPrivilege{{Name: "select"}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "root"}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "object_fence_reader"}}}, GrantOption: true}
				result, err := message.step(request)
				if err != nil || result.Rows != nil || result.Count != 0 {
					t.Fatal("actual retained ObjectGrant failed", err)
				}
				retained, err := message.op.Authorization()
				if err != nil {
					t.Fatal(err)
				}
				pending := retained.catalog.MustRelation(root.Address)
				if pending == table || retained.roleRecord == oldRoles || retained.pairedCatalog != retained.catalog || session.transactions.catalog.descriptors[root.Address] != pending {
					t.Fatal("utility did not stage real paired ACL/role/catalog roots")
				}
				found := false
				for _, entry := range pending.acl.Entries {
					if !entry.Grantee.Public && entry.Grantee.Role == reader {
						if entry.Grantor != dbRole || entry.Privileges != authorization.SelectPrivilege || entry.GrantOptions != authorization.SelectPrivilege {
							t.Fatal("actual grant lacks canonical owner origin and option", entry)
						}
						found = true
					}
				}
				if !found || e.catalog.state != oldCatalog || e.roles != oldRoles || !reflect.DeepEqual(table.acl, oldACL) {
					t.Fatal("staging lost the grant or published/mutated a prefix")
				}
				checkReader(retained, true)
				pendingRoles, pendingDB := retained.roleRecord, retained.catalog.Get(currentDatabaseAddress)
				if outcome == "database" {
					stage := e.BeginCatalogStage(e.CatalogRead())
					database := *e.catalog.state.MustDatabase(currentDatabaseAddress)
					if private {
						database.owner = actor.Ref
					} else {
						database.owner = oldRoles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
					}
					database.revision++
					if err := stage.Replace(&database); err != nil {
						t.Fatal(err)
					}
					ownershipUnitPublish(t, e, stage)
					e.DiscardCatalogStage(stage)
					for address, descriptor := range oldCatalog.Descriptors() {
						if address != currentDatabaseAddress && e.catalog.state.Get(address) != descriptor {
							t.Fatal("DB-only replacement changed object/associated identities")
						}
					}
					before = indexMaintenanceCapture(e)
				} else if outcome == "cancel" {
					cancel()
				}
				if e.roles != oldRoles || e.catalog.state.epoch != epoch || session.authorization.revision != sessionRevision {
					t.Fatal("replacement changed role/session/epoch authority")
				}
				err = message.end(nil)
				if outcome == "healthy" {
					if err != nil {
						t.Fatal("unchanged predecessor rejected legitimate retained grant", err)
					}
					current := catalogAuthorization(e, e.CatalogRead())
					checkReader(current, true)
					if e.catalog.state.Get(root.Address) != pending || e.roles != pendingRoles || !reflect.DeepEqual(e.storage[leaf].versions, rows) || !reflect.DeepEqual(pending.uniqueConstraints, oldKeys) {
						t.Fatal("healthy grant lost candidate or changed data/constraints")
					}
					for address, index := range before.roots {
						if e.indexRoots[address] != index {
							t.Fatal("healthy ACL-only grant changed index root")
						}
					}
					return
				}
				if outcome == "cancel" {
					if !errors.Is(err, context.Canceled) {
						t.Fatal("missing final cancellation veto", err)
					}
				} else {
					var conflict CatalogConflict
					if !errors.Is(err, ids.ErrCatalogReplaced) && !errors.As(err, &conflict) {
						t.Fatal("missing retained ObjectGrant DB predecessor veto", err)
					}
				}
				indexMaintenanceUnchanged(t, e, before)
				checkReader(catalogAuthorization(e, e.CatalogRead()), false)
				checkReader(retained, true)
				if e.roles != oldRoles || !reflect.DeepEqual(e.storage[leaf].versions, rows) || !reflect.DeepEqual(table.acl, oldACL) || !reflect.DeepEqual(table.uniqueConstraints, oldKeys) || retained.roleRecord != pendingRoles || retained.catalog.Get(currentDatabaseAddress) != pendingDB || auth.databasePredecessor != committed {
					t.Fatal("veto mutated old ACL/keys/storage or retained authority roots")
				}
			})
		}
	}
}
