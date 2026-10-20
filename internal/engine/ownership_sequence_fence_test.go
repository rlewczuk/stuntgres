package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Reuse the ownership roles and real sequence constructor, not a mock stage.
// Only the selected database descriptor supplies the source-owner privilege;
// the separate SET-only edge supplies successor eligibility, not source INHERIT.
func ownershipSequenceFenceFixture(t *testing.T, private bool) (*Engine, *Session, ids.ObjectRef, authorization.PrincipalRef, catalog.Descriptor, *catalogChanges) {
	t.Helper()
	e, bootstrap, actor, target, reader := ownershipUnitRoles()
	roles := cloneRoleFixture(e.roles.published)
	ordinary := fixtureRole(roles, actor)
	ordinary.Login, ordinary.ConnectionLimit = true, -1
	putFixtureRole(roles, ordinary)
	setFixtureEdges(roles, append(fixtureEdges(roles), authorization.Membership{Role: target, Member: actor, Grantor: bootstrap, Set: true}))
	e.roles.published = roles
	for _, query := range []string{
		"CREATE SEQUENCE ownership_fence_seq START 40 CACHE 3",
		"CREATE SEQUENCE ownership_fence_other START 100",
		"CREATE TABLE ownership_fence_sentinel(v INT PRIMARY KEY)",
		"INSERT INTO ownership_fence_sentinel VALUES(7)",
	} {
		ownershipUnitExec(t, e, query)
	}
	ref := roleTableRef(t, e, "ownership_fence_seq")
	setup := e.BeginCatalogStage(e.CatalogRead())
	dbRole := roles.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	sequence := *setup.Base.MustRelation(ref.Address)
	sequence.Owner = dbRole
	sequence.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		objectACLUnitEntry(dbRole, dbRole, authorization.UsagePrivilege|authorization.SelectPrivilege|authorization.UpdatePrivilege, 0),
		objectACLUnitEntry(dbRole, reader, authorization.UsagePrivilege, 0),
		objectACLUnitEntry(bootstrap, reader, authorization.SelectPrivilege, 0),
	}}
	sequence.ObjectRevision++
	if err := setup.Replace(&sequence); err != nil {
		t.Fatal(err)
	}
	// The successor genuinely owns the containing namespace. It does not
	// inherit pg_database_owner, and neither actor nor successor is superuser.
	if err := stageOwnership(setup, setup.Base.Get(sequence.Namespace()), target); err != nil {
		t.Fatal(err)
	}
	sentinel := roleTableRef(t, e, "ownership_fence_sentinel")
	if err := stageOwnership(setup, setup.Base.Get(sentinel.Address), actor); err != nil {
		t.Fatal(err)
	}
	if !private {
		if err := stageOwnership(setup, setup.Base.Get(catalog.CurrentDatabaseAddress), actor); err != nil {
			t.Fatal(err)
		}
	}
	ownershipUnitPublish(t, e, setup)
	e.DiscardCatalogStage(setup)
	committed := e.catalog.state.Get(catalog.CurrentDatabaseAddress)
	var changes *catalogChanges
	if private {
		// Honest installed predecessor, distinct selected owner-private authority;
		// this is the package overlay seam, not new database SQL admission.
		changes = session.NewJournal()
		database := *committed.(*catalog.Database)
		database.Owner, database.ObjectRevision = actor, database.Revision()+1
		changes.Descriptors[catalog.CurrentDatabaseAddress] = &database
		changes.Originals[catalog.CurrentDatabaseAddress] = committed
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
	return e, session, ref, target, committed, changes
}

func TestRoleOwnershipSequenceDatabaseFence(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, phase := range []string{"before-statement-finish", "outer-message", "healthy"} {
			name := map[bool]string{false: "committed", true: "private"}[private] + "/" + phase
			t.Run(name, func(t *testing.T) {
				e, session, ref, target, committed, changes := ownershipSequenceFenceFixture(t, private)
				message := batchUnitStart(t, session, context.Background())
				defer message.end(context.Canceled)
				if private {
					session.Transactions.catalog = changes
				}
				auth, err := operationAuthorization(message.op)
				if err != nil {
					t.Fatal(err)
				}
				actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
				if err != nil || !actor.Login || actor.Superuser || fixtureRole(auth.RoleRecord, target).Superuser {
					t.Fatal("fixture is not an ordinary login actor and successor", err)
				}
				dbRole := auth.Roles.LookupRef("pg_database_owner")
				old := auth.Catalog.MustRelation(ref.Address)
				selectedDB := auth.Catalog.Get(catalog.CurrentDatabaseAddress)
				if old.Owner != dbRole || old.Owner == actor.Ref || selectedDB.(*catalog.Database).Owner != actor.Ref || auth.DatabasePredecessor != committed || auth.PairedCatalog != auth.Catalog {
					t.Fatal("fixture lacks canonical paired selected DB ownership authority")
				}
				if private && selectedDB == committed {
					t.Fatal("private authority is not distinct from its exact committed predecessor")
				}
				for edge := range auth.Roles.Edges() {
					if edge.Role == dbRole && edge.Member == actor.Ref {
						t.Fatal("explicit membership bypasses implicit database ownership")
					}
				}
				if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
					t.Fatal("selected database owner cannot own actual sequence", err)
				}
				if allowed, err := auth.Roles.CanSetRole(actor.Ref, target); err != nil || !allowed {
					t.Fatal("successor is not SET eligible", err)
				}
				targetAuth := auth
				targetAuth.State.CurrentUser = target
				if err := targetAuth.CheckCreate(e.catalog.Domain.Ref(ref.Epoch, old.Namespace())); err != nil {
					t.Fatal("successor lacks actual namespace CREATE", err)
				}
				retainedACL := authorization.ACLValue{Explicit: old.Security.ACL.Explicit, Entries: append([]authorization.ACLEntry(nil), old.Security.ACL.Entries...)}
				retainedOptions := *old.Sequence
				oldRoles, sessionRevision, epoch := e.roles.published, session.Authorization.revision, e.catalog.state.Epoch()
				originalCatalog := e.catalog.state
				// A real nontransactional draw must survive rollback without another
				// draw, cache reset, generator replacement or currval change.
				result, err := message.step(statement(t, "SELECT nextval('ownership_fence_seq')"))
				if err != nil {
					t.Fatal(err)
				}
				values := tuples(result)
				result.Rows.Close()
				if !reflect.DeepEqual(values, [][]driver.Value{{int64(40)}}) {
					t.Fatal("actual sequence draw", values)
				}
				generator := e.storage.Sequences[ref.Address]
				generatorValue := *generator
				memory := payloadOf(session).sequences.Memory(ref.Address)
				memoryValue, lastSequence := *memory, payloadOf(session).sequences.Last()
				if !generator.Called || generator.Last != 42 || !currvalIs(memory, 40) || memory.CachedTail() != 2 {
					t.Fatal("fixture lacks actual reserved counter/currval/cache state")
				}
				// A substantive retained prefix proves whole-message veto, not just
				// omission of the final sequence descriptor.
				if _, err := message.step(statement(t, "INSERT INTO ownership_fence_sentinel VALUES(9)")); err != nil {
					t.Fatal(err)
				}
				message.scope, err = message.scope.NextStatement()
				if err != nil {
					t.Fatal(err)
				}
				before := indexMaintenanceCapture(e)
				sentinel := roleTableRef(t, e, "ownership_fence_sentinel").Address
				rows := append([]storage.RowVersion(nil), e.storage.Relations[sentinel].Versions...)
				for i := range rows {
					rows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
				}
				generators := make(map[ids.ObjectAddress]*storage.SequenceGenerator, len(e.storage.Sequences))
				generatorValues := make(map[ids.ObjectAddress]storage.SequenceGenerator, len(e.storage.Sequences))
				for address, g := range e.storage.Sequences {
					generators[address], generatorValues[address] = g, *g
				}
				replaceDatabase := func() {
					stage := e.BeginCatalogStage(e.CatalogRead())
					database := *e.catalog.state.MustDatabase(catalog.CurrentDatabaseAddress)
					if private {
						// Even equal owner identities cannot excuse replacing the
						// exact installed predecessor of a distinct private view.
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
					if e.roles.published != oldRoles || session.Authorization.revision != sessionRevision || e.catalog.state.Epoch() != epoch {
						t.Fatal("DB-only replacement changed role/session/epoch authority")
					}
					for address, descriptor := range before.catalog.Descriptors() {
						if address != catalog.CurrentDatabaseAddress && e.catalog.state.Get(address) != descriptor {
							t.Fatal("DB-only replacement changed an unrelated descriptor")
						}
					}
					before = indexMaintenanceCapture(e)
				}
				syntax := &parser.AlterSequence{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "ownership_fence_seq"}}}, Action: "owner", Target: parser.Identifier{Text: "ownership_target"}}
				if _, err := message.scope.Execute(syntax, 0, nil); err != nil {
					t.Fatal("actual OWNER handler failed before publication gate", err)
				}
				candidate := message.scope.catalog
				if candidate == nil {
					t.Fatal("actual OWNER handler did not stage catalog changes")
				}
				staged, ok := candidate.Descriptors[ref.Address].(*catalog.Relation)
				reader, bootstrap := oldRoles.Root.Read(authorization.PrincipalRef{}).LookupRef("ownership_reader"), oldRoles.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
				wantACL := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
					objectACLUnitEntry(target, target, authorization.UsagePrivilege|authorization.SelectPrivilege|authorization.UpdatePrivilege, 0),
					objectACLUnitEntry(target, reader, authorization.UsagePrivilege, 0),
					objectACLUnitEntry(bootstrap, reader, authorization.SelectPrivilege, 0),
				}}
				if !ok || staged == old || staged.Owner != target || !reflect.DeepEqual(staged.Security.ACL, wantACL) || *staged.Sequence != retainedOptions || candidate.Generators[ref.Address] != nil || e.catalog.state != originalCatalog {
					t.Fatal("actual OWNER handler did not stage descriptor/ACL-only transfer without a prefix")
				}
				var checkpoint *rangePartitionPhaseContext
				if phase == "before-statement-finish" {
					// Installed only after real Execute has returned its candidate.
					// Finish's operation check holds no engine lock. The independent
					// native catalog stage has no session authorization callback.
					checkpoint = &rangePartitionPhaseContext{Context: message.op.Context(), target: "(*StatementScope).Finish", effect: replaceDatabase}
					message.op.Ctx = checkpoint
				}
				finishErr := message.scope.Finish(nil)
				if phase == "before-statement-finish" && !checkpoint.entered.Load() {
					t.Fatal("actual Statement Finish did not reach context barrier")
				}
				if phase == "outer-message" {
					if finishErr != nil {
						t.Fatal("retained Statement Finish failed before DB replacement", finishErr)
					}
					if e.catalog.state != originalCatalog || session.Transactions.catalog != candidate || len(session.Transactions.writes[sentinel]) != 1 {
						t.Fatal("retained Finish installed a prefix or lost actual pending effects")
					}
					replaceDatabase()
				}
				err = message.end(finishErr)
				if phase == "healthy" {
					if err != nil || e.catalog.state.Get(ref.Address) != staged || !reflect.DeepEqual(staged.Security.ACL, wantACL) || len(e.storage.Relations[sentinel].Versions) != 2 {
						t.Fatal("healthy selected authority did not publish actual transfer and prefix", err)
					}
					if private && e.catalog.state.Get(catalog.CurrentDatabaseAddress) != selectedDB {
						t.Fatal("healthy private authority did not retain its own DB candidate")
					}
				} else {
					var conflict catalog.Conflict
					if !errors.Is(err, ids.ErrCatalogReplaced) && !errors.As(err, &conflict) {
						t.Fatal("missing sequence OWNER DB predecessor veto", err)
					}
					indexMaintenanceUnchanged(t, e, before)
					if e.catalog.state.Get(ref.Address) != old || !reflect.DeepEqual(e.storage.Relations[sentinel].Versions, rows) {
						t.Fatal("veto installed sequence owner/ACL or retained row prefix")
					}
				}
				if e.roles.published != oldRoles || session.Authorization.revision != sessionRevision || e.catalog.state.Epoch() != epoch || old.Owner != dbRole || !reflect.DeepEqual(old.Security.ACL, retainedACL) || *old.Sequence != retainedOptions || auth.Catalog.Get(ref.Address) != old || auth.Catalog.Get(catalog.CurrentDatabaseAddress) != selectedDB || auth.DatabasePredecessor != committed || originalCatalog.Get(catalog.CurrentDatabaseAddress) != committed {
					t.Fatal("publication mutated immutable retained authority or sequence roots")
				}
				if e.storage.Sequences[ref.Address] != generator || *generator != generatorValue || payloadOf(session).sequences.Memory(ref.Address) != memory || *memory != memoryValue || payloadOf(session).sequences.Last() != lastSequence {
					t.Fatal("OWNER publication changed actual nontransactional counter/cache/currval")
				}
				for address, g := range generators {
					if e.storage.Sequences[address] != g || *g != generatorValues[address] {
						t.Fatal("OWNER publication changed unrelated sequence generators")
					}
				}
			})
		}
	}
}
