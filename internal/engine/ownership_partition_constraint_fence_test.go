package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This fixture reuses the real partition/key/storage construction, then drives
// the actual early-return handler rather than publishing a hand-built drop stage.
func ownershipPartitionConstraintFixture(t *testing.T, private bool) (*Engine, *session.Session, ids.ObjectRef, catalog.Descriptor, *session.Journal) {
	t.Helper()
	e, unused := partitionConstraintDropPublicationStage(t, true)
	e.DiscardCatalogStage(unused)
	roles := cloneRoleFixture(e.roles.Published)
	actor := e.roles.Domain.Principal(50000)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: actor, Name: "partition_db_actor", Login: true, Inherit: true, ConnectionLimit: -1})

	e.roles.PublishLocked(roles)
	root, leaf := roleTableRef(t, e, "root"), roleTableRef(t, e, "leaf")
	setup := e.BeginCatalogStage(e.CatalogRead())
	dbOwner := roles.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	for _, ref := range []ids.ObjectRef{root, leaf} {
		if err := role.StageOwnership(setup, setup.Base.Get(ref.Address), dbOwner); err != nil {
			t.Fatal(err)
		}
	}
	if !private {
		if err := role.StageOwnership(setup, setup.Base.Get(catalog.CurrentDatabaseAddress), actor); err != nil {
			t.Fatal(err)
		}
	}
	ownershipUnitPublish(t, e, setup)
	e.DiscardCatalogStage(setup)
	committed := e.catalog.State.Get(catalog.CurrentDatabaseAddress)
	var changes *session.Journal
	if private {
		// Honest predecessor plus a different selected private descriptor. This
		// tests catalog authority, not SQL admission for transactional DB OWNER.
		changes = session.NewJournal()
		database := *committed.(*catalog.Database)
		database.Owner, database.ObjectRevision = actor, database.Revision()+1
		changes.Descriptors[catalog.CurrentDatabaseAddress] = &database
		changes.Originals[catalog.CurrentDatabaseAddress] = committed
	}
	sess, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: actor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return e, sess, root, committed, changes
}

func TestRoleOwnershipPartitionConstraintDatabaseFence(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, phase := range []string{"during-stage", "outer-message", "healthy"} {
			name := map[bool]string{false: "committed", true: "private"}[private] + "/" + phase
			t.Run(name, func(t *testing.T) {
				e, session, root, committed, changes := ownershipPartitionConstraintFixture(t, private)
				message := batchUnitStart(t, session, context.Background())
				defer message.end(context.Canceled)
				if private {
					session.Transactions.Catalog = changes
				}
				auth, err := operationAuthorization(message.op)
				if err != nil {
					t.Fatal(err)
				}
				actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
				if err != nil || actor.Superuser {
					t.Fatal("fixture is not an ordinary actor", err)
				}
				dbRole := auth.Roles.LookupRef("pg_database_owner")
				table := auth.Catalog.MustRelation(root.Address)
				if table.Owner != dbRole || auth.DatabasePredecessor != committed || auth.PairedCatalog != auth.Catalog {
					t.Fatal("fixture did not capture canonical paired DB authority")
				}
				if private && auth.Catalog.Get(catalog.CurrentDatabaseAddress) == committed {
					t.Fatal("private authority is not distinct from committed predecessor")
				}
				if err := auth.Check(access.Requirement{Object: root, OwnerOnly: true}); err != nil {
					t.Fatal("selected DB owner did not authorize actual partition", err)
				}
				oldRoles, sessionRevision, epoch := e.roles.Published, session.Authorization.Revision, e.catalog.State.Epoch()
				before := indexMaintenanceCapture(e)
				leaf := roleTableRef(t, e, "leaf").Address
				rows := append([]storage.RowVersion(nil), e.storage.Relations[leaf].Versions...)
				for i := range rows {
					rows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
				}
				if len(rows) != 1 || len(table.Constraints.Unique) != 1 || len(e.catalog.State.MustRelation(leaf).Constraints.Unique) != 2 {
					t.Fatal("fixture lacks actual rows or recursive/local keys")
				}
				rootKey := table.Constraints.Unique[0]
				selected := map[ids.ObjectAddress]bool{rootKey.Index: true}
				for _, key := range e.catalog.State.MustRelation(leaf).Constraints.Unique {
					if key.Parent == rootKey.OID {
						selected[key.Index] = true
					}
				}
				if len(selected) != 2 {
					t.Fatal("fixture lacks canonical child backing-index association")
				}
				replaceDatabase := func() {
					stage := e.BeginCatalogStage(e.CatalogRead())
					database := *e.catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
					if private {
						database.Owner = actor.Ref
					} else {
						database.Owner = oldRoles.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
					}
					database.ObjectRevision++
					if err := stage.Replace(&database); err != nil {
						t.Fatal(err)
					}
					ownershipUnitPublish(t, e, stage)
					e.DiscardCatalogStage(stage)
					if e.roles.Published != oldRoles || session.Authorization.Revision != sessionRevision || e.catalog.State.Epoch() != epoch {
						t.Fatal("DB-only replacement changed unrelated authority")
					}
					for address, descriptor := range before.catalog.Descriptors() {
						if address != catalog.CurrentDatabaseAddress && e.catalog.State.Get(address) != descriptor {
							t.Fatal("DB-only replacement changed target or associated identities")
						}
					}
					before = indexMaintenanceCapture(e)
				}
				var checkpoint *rangePartitionPhaseContext
				if phase == "during-stage" {
					checkpoint = &rangePartitionPhaseContext{Context: message.op.Context(), target: "(*Engine).ValidateCatalogStage", effect: replaceDatabase}
					message.op.Ctx = checkpoint
				}
				syntax := &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "root"}}}, Actions: []parser.AlterTableAction{{Action: parser.DropConstraint, Constraint: parser.Identifier{Text: "root_key"}, Mode: parser.DropRestrict}}}
				_, executeErr := message.scope.Execute(syntax, 0, nil)
				if phase == "during-stage" {
					if !checkpoint.entered.Load() {
						t.Fatal("actual handler did not reach post-staging validation checkpoint")
					}
				} else {
					if executeErr != nil {
						t.Fatal("actual drop candidate could not be staged", executeErr)
					}
					candidate := message.scope.catalog
					if candidate == nil || len(candidate.Descriptors[root.Address].(*catalog.Relation).Constraints.Unique) != 0 || len(candidate.Descriptors[leaf].(*catalog.Relation).Constraints.Unique) != 1 {
						t.Fatal("actual handler did not stage recursive key/index deletion")
					}
					for address := range selected {
						if descriptor, observed := candidate.Descriptors[address]; !observed || descriptor != nil {
							t.Fatal("actual handler did not stage canonical backing-index tombstone")
						}
					}
					if phase == "outer-message" {
						if err := message.scope.Finish(nil); err != nil {
							t.Fatal("statement boundary failed before DB replacement", err)
						}
						replaceDatabase()
					}
				}
				err = message.end(executeErr)
				if phase == "healthy" {
					if err != nil {
						t.Fatal("unchanged committed predecessor rejected selected authority", err)
					}
					if len(e.catalog.State.MustRelation(root.Address).Constraints.Unique) != 0 || len(e.catalog.State.MustRelation(leaf).Constraints.Unique) != 1 || !reflect.DeepEqual(e.storage.Relations[leaf].Versions, rows) {
						t.Fatal("healthy drop changed rows or retained selected keys")
					}
					for address, descriptor := range before.catalog.Descriptors() {
						index, ok := descriptor.(*catalog.Relation)
						if !ok || index.SQLIndex == nil {
							continue
						}
						if selected[address] {
							if e.catalog.State.Get(address) != nil {
								t.Fatal("healthy drop retained actual selected backing index")
							}
						} else if e.catalog.State.Get(address) != descriptor {
							t.Fatal("healthy drop changed independent index identity")
						}
					}
					return
				}
				var conflict catalog.Conflict
				if !errors.Is(err, ids.ErrCatalogReplaced) && !errors.As(err, &conflict) {
					t.Fatal("missing partition constraint DB predecessor veto", err)
				}
				indexMaintenanceUnchanged(t, e, before)
				if e.roles.Published != oldRoles || !reflect.DeepEqual(e.storage.Relations[leaf].Versions, rows) || len(table.Constraints.Unique) != 1 || auth.DatabasePredecessor != committed || auth.Catalog.Get(root.Address) != table || len(auth.Catalog.MustRelation(leaf).Constraints.Unique) != 2 {
					t.Fatal("veto mutated retained roles, constraints or storage")
				}
			})
		}
	}
}
