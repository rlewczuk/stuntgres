package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// This fixture reuses the real partition/key/storage construction, then drives
// the actual early-return handler rather than publishing a hand-built drop stage.
func ownershipPartitionConstraintFixture(t *testing.T, private bool) (*Engine, *Session, ids.ObjectRef, Descriptor, *catalogChanges) {
	t.Helper()
	e, unused := partitionConstraintDropPublicationStage(t, true)
	e.DiscardCatalogStage(unused)
	roles := cloneRoleFixture(e.roles)
	actor := e.authorizationDomain.Principal(50000)
	putFixtureRole(roles, authorization.RoleDescriptor{Ref: actor, Name: "partition_db_actor", Login: true, Inherit: true, ConnectionLimit: -1})

	e.roles = roles
	root, leaf := roleTableRef(t, e, "root"), roleTableRef(t, e, "leaf")
	setup := e.BeginCatalogStage(e.CatalogRead())
	dbOwner := roles.root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	for _, ref := range []ids.ObjectRef{root, leaf} {
		if err := stageOwnership(setup, setup.Base.Get(ref.Address), dbOwner); err != nil {
			t.Fatal(err)
		}
	}
	if !private {
		if err := stageOwnership(setup, setup.Base.Get(currentDatabaseAddress), actor); err != nil {
			t.Fatal(err)
		}
	}
	ownershipUnitPublish(t, e, setup)
	e.DiscardCatalogStage(setup)
	committed := e.catalog.state.Get(currentDatabaseAddress)
	var changes *catalogChanges
	if private {
		// Honest predecessor plus a different selected private descriptor. This
		// tests catalog authority, not SQL admission for transactional DB OWNER.
		changes = newCatalogChanges()
		database := *committed.(*databaseDescriptor)
		database.Owner, database.ObjectRevision = actor, database.Revision()+1
		changes.descriptors[currentDatabaseAddress] = &database
		changes.originals[currentDatabaseAddress] = committed
	}
	session, err := e.StartSession(context.Background(), SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: actor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		session.RequestEnd(CleanClose)
		if err := session.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return e, session, root, committed, changes
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
				if table.Owner != dbRole || auth.databasePredecessor != committed || auth.pairedCatalog != auth.catalog {
					t.Fatal("fixture did not capture canonical paired DB authority")
				}
				if private && auth.catalog.Get(currentDatabaseAddress) == committed {
					t.Fatal("private authority is not distinct from committed predecessor")
				}
				if err := auth.Check(AccessRequirement{Object: root, OwnerOnly: true}); err != nil {
					t.Fatal("selected DB owner did not authorize actual partition", err)
				}
				oldRoles, sessionRevision, epoch := e.roles, session.authorization.revision, e.catalog.state.Epoch()
				before := indexMaintenanceCapture(e)
				leaf := roleTableRef(t, e, "leaf").Address
				rows := append([]rowVersion(nil), e.storage[leaf].versions...)
				for i := range rows {
					rows[i].values = append([]sqltype.Value(nil), rows[i].values...)
				}
				if len(rows) != 1 || len(table.UniqueConstraints) != 1 || len(e.catalog.state.MustRelation(leaf).UniqueConstraints) != 2 {
					t.Fatal("fixture lacks actual rows or recursive/local keys")
				}
				rootKey := table.UniqueConstraints[0]
				selected := map[ids.ObjectAddress]bool{rootKey.Index: true}
				for _, key := range e.catalog.state.MustRelation(leaf).UniqueConstraints {
					if key.Parent == rootKey.OID {
						selected[key.Index] = true
					}
				}
				if len(selected) != 2 {
					t.Fatal("fixture lacks canonical child backing-index association")
				}
				replaceDatabase := func() {
					stage := e.BeginCatalogStage(e.CatalogRead())
					database := *e.catalog.state.MustDatabase(currentDatabaseAddress)
					if private {
						database.Owner = actor.Ref
					} else {
						database.Owner = oldRoles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
					}
					database.ObjectRevision++
					if err := stage.Replace(&database); err != nil {
						t.Fatal(err)
					}
					ownershipUnitPublish(t, e, stage)
					e.DiscardCatalogStage(stage)
					if e.roles != oldRoles || session.authorization.revision != sessionRevision || e.catalog.state.Epoch() != epoch {
						t.Fatal("DB-only replacement changed unrelated authority")
					}
					for address, descriptor := range before.catalog.Descriptors() {
						if address != currentDatabaseAddress && e.catalog.state.Get(address) != descriptor {
							t.Fatal("DB-only replacement changed target or associated identities")
						}
					}
					before = indexMaintenanceCapture(e)
				}
				var checkpoint *rangePartitionPhaseContext
				if phase == "during-stage" {
					checkpoint = &rangePartitionPhaseContext{Context: message.op.Context(), target: "(*Engine).ValidateCatalogStage", effect: replaceDatabase}
					message.op.ctx = checkpoint
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
					if candidate == nil || len(candidate.descriptors[root.Address].(*relationDescriptor).UniqueConstraints) != 0 || len(candidate.descriptors[leaf].(*relationDescriptor).UniqueConstraints) != 1 {
						t.Fatal("actual handler did not stage recursive key/index deletion")
					}
					for address := range selected {
						if descriptor, observed := candidate.descriptors[address]; !observed || descriptor != nil {
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
					if len(e.catalog.state.MustRelation(root.Address).UniqueConstraints) != 0 || len(e.catalog.state.MustRelation(leaf).UniqueConstraints) != 1 || !reflect.DeepEqual(e.storage[leaf].versions, rows) {
						t.Fatal("healthy drop changed rows or retained selected keys")
					}
					for address, descriptor := range before.catalog.Descriptors() {
						index, ok := descriptor.(*relationDescriptor)
						if !ok || index.SQLIndex == nil {
							continue
						}
						if selected[address] {
							if e.catalog.state.Get(address) != nil {
								t.Fatal("healthy drop retained actual selected backing index")
							}
						} else if e.catalog.state.Get(address) != descriptor {
							t.Fatal("healthy drop changed independent index identity")
						}
					}
					return
				}
				var conflict CatalogConflict
				if !errors.Is(err, ids.ErrCatalogReplaced) && !errors.As(err, &conflict) {
					t.Fatal("missing partition constraint DB predecessor veto", err)
				}
				indexMaintenanceUnchanged(t, e, before)
				if e.roles != oldRoles || !reflect.DeepEqual(e.storage[leaf].versions, rows) || len(table.UniqueConstraints) != 1 || auth.databasePredecessor != committed || auth.catalog.Get(root.Address) != table || len(auth.catalog.MustRelation(leaf).UniqueConstraints) != 2 {
					t.Fatal("veto mutated retained roles, constraints or storage")
				}
			})
		}
	}
}
