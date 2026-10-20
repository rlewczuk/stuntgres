package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"maps"
	"reflect"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

// Creation uses a separate real namespace: its explicit ACL grants no PUBLIC
// CREATE, and its only owner is pg_database_owner. The actor's direct ownership
// of the OWNED BY table cannot supply the earlier namespace CREATE privilege.
func ownershipManualCreationFixture(t *testing.T, e *Engine, actor authorization.PrincipalRef) (ids.ObjectAddress, ids.ObjectRef) {
	t.Helper()
	ownershipUnitExec(t, e, "CREATE SCHEMA ownership_fence_creation")
	ownershipUnitExec(t, e, "CREATE TABLE ownership_fence_creation.owned_table(v INT)")
	ns := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "ownership_fence_creation"})
	table := roleTableRef(t, e, "ownership_fence_creation.owned_table")
	stage := e.BeginCatalogStage(e.CatalogRead())
	namespace := *stage.Base.MustNamespace(ns)
	namespace.Owner = e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("pg_database_owner")
	namespace.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(namespace.Owner, namespace.Owner, authorization.UsagePrivilege|authorization.CreatePrivilege, 0)}}
	namespace.ObjectRevision++
	if err := stage.Replace(&namespace); err != nil {
		t.Fatal(err)
	}
	if err := stageOwnership(stage, stage.Base.Get(table.Address), actor); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	e.DiscardCatalogStage(stage)
	return ns, roleTableRef(t, e, "ownership_fence_creation.owned_table")
}

func ownershipManualName(parts ...string) parser.ObjectName {
	name := parser.ObjectName{}
	for _, part := range parts {
		name.Parts = append(name.Parts, parser.Identifier{Text: part})
	}
	return name
}

// The snapshots are independent of the pending candidate. They include the
// genuinely consumed generator/cache/currval and the retained INSERT prefix.
// A database-only replacement must not mask an installed descriptor or row root.
type ownershipManualSnapshot struct {
	roots           indexMaintenanceSnapshot
	roles           *access.RoleRecord
	revision        uint64
	generators      map[ids.ObjectAddress]*storage.SequenceGenerator
	generatorValues map[ids.ObjectAddress]storage.SequenceGenerator
	memory          *sequence.Memory
	memoryValue     sequence.Memory
	last            ids.ObjectAddress
	rows            map[ids.ObjectAddress][]storage.RowVersion
}

func ownershipManualCapture(e *Engine, session *Session, sequence ids.ObjectRef) ownershipManualSnapshot {
	snapshot := ownershipManualSnapshot{
		roots: indexMaintenanceCapture(e), roles: e.roles.published, revision: session.authorization.revision,
		generators: make(map[ids.ObjectAddress]*storage.SequenceGenerator), generatorValues: make(map[ids.ObjectAddress]storage.SequenceGenerator),
		memory: session.sequences.Memory(sequence.Address), last: session.sequences.Last(),
		rows: make(map[ids.ObjectAddress][]storage.RowVersion),
	}
	snapshot.memoryValue = *snapshot.memory
	for address, generator := range e.storage.Sequences {
		snapshot.generators[address], snapshot.generatorValues[address] = generator, *generator
	}
	for address, stored := range e.storage.Relations {
		rows := append([]storage.RowVersion(nil), stored.Versions...)
		for i := range rows {
			rows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
		}
		snapshot.rows[address] = rows
	}
	return snapshot
}

func ownershipManualStart(t *testing.T, e *Engine, session *Session, sequence ids.ObjectRef, changes *catalogChanges) (*batchUnitMessage, AuthorizationView, ownershipManualSnapshot) {
	t.Helper()
	message := batchUnitStart(t, session, context.Background())
	t.Cleanup(func() { message.end(context.Canceled) })
	if changes != nil {
		session.transactions.catalog = changes
	}
	auth, err := operationAuthorization(message.op)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil || actor.Superuser || !actor.Login || auth.PairedCatalog != auth.Catalog || auth.Catalog.MustDatabase(catalog.CurrentDatabaseAddress).Owner != actor.Ref {
		t.Fatal("manual utility fixture lacks ordinary paired selected DB authority", err)
	}
	result, err := message.step(statement(t, "SELECT nextval('ownership_fence_seq')"))
	if err != nil {
		t.Fatal(err)
	}
	values := tuples(result)
	result.Rows.Close()
	if !reflect.DeepEqual(values, [][]driver.Value{{int64(40)}}) || e.storage.Sequences[sequence.Address].Last != 42 || session.sequences.Memory(sequence.Address).CachedTail() != 2 {
		t.Fatal("fixture did not consume actual nontransactional CACHE3 sequence", values)
	}
	if _, err := message.step(statement(t, "INSERT INTO ownership_fence_sentinel VALUES(9)")); err != nil {
		t.Fatal(err)
	}
	message.scope, err = message.scope.NextStatement()
	if err != nil {
		t.Fatal(err)
	}
	return message, auth, ownershipManualCapture(e, session, sequence)
}

func ownershipManualReplaceDatabase(t *testing.T, e *Engine, session *Session, before *ownershipManualSnapshot, private bool) {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	database := *e.catalog.state.MustDatabase(catalog.CurrentDatabaseAddress)
	if private {
		database.Owner = session.authorization.state.CurrentUser
	} else {
		database.Owner = e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	}
	database.ObjectRevision++
	if err := stage.Replace(&database); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, stage)
	e.DiscardCatalogStage(stage)
	if e.roles.published != before.roles || session.authorization.revision != before.revision || e.catalog.state.Epoch() != before.roots.catalog.Epoch() {
		t.Fatal("database replacement changed role/session/epoch authority")
	}
	for address, descriptor := range before.roots.catalog.Descriptors() {
		if address != catalog.CurrentDatabaseAddress && e.catalog.state.Get(address) != descriptor {
			t.Fatal("database replacement changed unrelated descriptor", address)
		}
	}
	for address, generator := range before.generators {
		if e.storage.Sequences[address] != generator || *generator != before.generatorValues[address] {
			t.Fatal("database replacement changed actual generator", address)
		}
	}
	for address, root := range before.roots.roots {
		if e.storage.IndexRoots[address] != root {
			t.Fatal("database replacement changed index root")
		}
	}
	for address, storage := range before.roots.stores {
		if e.storage.Relations[address] != storage || !reflect.DeepEqual(storage.Versions, before.rows[address]) {
			t.Fatal("database replacement changed storage/rows")
		}
	}
	before.roots = indexMaintenanceCapture(e)
}

func ownershipManualRetainedUnchanged(t *testing.T, e *Engine, session *Session, sequence ids.ObjectRef, auth AuthorizationView, committed catalog.Descriptor, old *catalog.Relation, retainedACL authorization.ACLValue, retainedOptions catalog.SequenceOptions, before ownershipManualSnapshot, dropped bool) {
	t.Helper()
	if e.roles.published != before.roles || session.authorization.revision != before.revision || e.catalog.state.Epoch() != auth.Catalog.Epoch() || auth.DatabasePredecessor != committed || auth.Catalog.Get(sequence.Address) != old || old.Owner != auth.Roles.LookupRef("pg_database_owner") || old.Comment != "retained sequence comment" || !reflect.DeepEqual(old.Security.ACL, retainedACL) || *old.Sequence != retainedOptions {
		t.Fatal("manual utility mutated retained sequence/ACL/paired authority")
	}
	if session.sequences.Memory(sequence.Address) != before.memory || *before.memory != before.memoryValue || session.sequences.Last() != before.last {
		t.Fatal("manual utility changed actual currval/cache/lastval memory")
	}
	for address, generator := range before.generators {
		if dropped && address == sequence.Address {
			if e.storage.Sequences[address] != nil || *generator != before.generatorValues[address] {
				t.Fatal("healthy DROP retained generator or mutated old generator")
			}
			continue
		}
		if e.storage.Sequences[address] != generator || *generator != before.generatorValues[address] {
			t.Fatal("manual utility changed retained/unrelated generator")
		}
	}
}

func TestRoleOwnershipManualUtilityDatabaseFence(t *testing.T) {
	for _, utility := range []string{"create-sequence", "drop-sequence", "comment-sequence", "create-enum"} {
		for _, private := range []bool{false, true} {
			for _, phase := range []string{"before-statement-finish", "outer-message", "healthy"} {
				t.Run(utility+"/"+map[bool]string{false: "committed", true: "private"}[private]+"/"+phase, func(t *testing.T) {
					e, session, sequence, _, committed, changes := ownershipSequenceFenceFixture(t, private)
					ns, _ := ownershipManualCreationFixture(t, e, session.authorization.state.CurrentUser)
					setup := e.BeginCatalogStage(e.CatalogRead())
					old := *setup.Base.MustRelation(sequence.Address)
					old.Comment, old.ObjectRevision = "retained sequence comment", old.Revision()+1
					if err := setup.Replace(&old); err != nil {
						t.Fatal(err)
					}
					ownershipUnitPublish(t, e, setup)
					e.DiscardCatalogStage(setup)
					message, auth, before := ownershipManualStart(t, e, session, sequence, changes)
					selectedDB := auth.Catalog.Get(catalog.CurrentDatabaseAddress)
					if auth.DatabasePredecessor != committed || private && selectedDB == committed || !private && selectedDB != committed {
						t.Fatal("dishonest selected/committed DB pair")
					}
					if err := auth.CheckCreate(e.catalog.Domain.Ref(auth.Catalog.Epoch(), ns)); err != nil {
						t.Fatal("canonical DB owner cannot CREATE in strict namespace", err)
					}
					withoutDB := auth
					withoutDB.State.CurrentUser = auth.Roles.LookupRef("ownership_reader")
					if err := withoutDB.CheckCreate(e.catalog.Domain.Ref(auth.Catalog.Epoch(), ns)); err == nil {
						t.Fatal("namespace PUBLIC/direct rights bypass DB-derived CREATE")
					}
					retainedACL := authorization.ACLValue{Explicit: old.Security.ACL.Explicit, Entries: append([]authorization.ACLEntry(nil), old.Security.ACL.Entries...)}
					retainedOptions := *old.Sequence
					var syntax parser.Statement
					newName := ownershipManualName("ownership_fence_creation", "new_sequence")
					switch utility {
					case "create-sequence":
						syntax = &parser.CreateSequence{Name: newName, Persistence: "logged", Options: []parser.SequenceOption{{Name: "start", Value: 51}, {Name: "increment", Value: 2}, {Name: "cache", Value: 4}}}
					case "drop-sequence":
						syntax = &parser.DropSequence{Names: []parser.ObjectName{ownershipManualName("ownership_fence_seq")}, Mode: parser.DropRestrict}
					case "comment-sequence":
						syntax = &parser.Comment{Kind: parser.CommentSequence, Name: ownershipManualName("ownership_fence_seq"), Text: "new sequence comment"}
					case "create-enum":
						syntax = &parser.CreateEnum{Name: ownershipManualName("ownership_fence_creation", "new_enum"), Labels: []parser.EnumLabel{{Text: "first"}, {Text: "second"}}}
					}
					if _, err := message.scope.Execute(syntax, 0, nil); err != nil {
						t.Fatal("actual manual utility failed before publication gate", err)
					}
					candidate := message.scope.catalog
					if candidate == nil || e.catalog.state != before.roots.catalog {
						t.Fatal("actual utility did not retain its candidate without prefix")
					}
					var created *catalog.Relation
					var enum, array *catalog.Type
					switch utility {
					case "create-sequence":
						for _, descriptor := range candidate.Descriptors {
							if d, ok := descriptor.(*catalog.Relation); ok && d.Name() == "new_sequence" {
								created = d
							}
						}
						if created == nil || created.Namespace() != ns || created.Owner != auth.State.CurrentUser || created.Sequence.Start != 51 || created.Sequence.Increment != 2 || created.Sequence.Cache != 4 || created.Sequence.Owned != (ids.ObjectAddress{}) || created.Security.ACL.Explicit {
							t.Fatal("CREATE did not stage actual options/owner/default ACL")
						}
						g := candidate.Generators[created.Address()]
						if g == nil || g.Last != 51 || g.Called || g.Generation != created.Revision() {
							t.Fatal("CREATE did not stage actual uncalled generator")
						}
					case "drop-sequence":
						if d, staged := candidate.Descriptors[sequence.Address]; !staged || d != nil || candidate.Generators[sequence.Address] != nil {
							t.Fatal("DROP did not stage actual sequence tombstone")
						}
					case "comment-sequence":
						d := candidate.Descriptors[sequence.Address].(*catalog.Relation)
						if d == &old || d.Comment != "new sequence comment" || d.Owner != old.Owner || !reflect.DeepEqual(d.Security.ACL, retainedACL) || *d.Sequence != retainedOptions || candidate.Generators[sequence.Address] != nil {
							t.Fatal("COMMENT did not stage comment-only descriptor replacement")
						}
					case "create-enum":
						for _, descriptor := range candidate.Descriptors {
							if d, ok := descriptor.(*catalog.Type); ok && d.SQLType.IsEnum() && d.Name() == "new_enum" {
								enum = d
							}
						}
						if enum == nil {
							t.Fatal("CREATE ENUM did not stage actual enum descriptor")
						}
						array = candidate.Descriptors[ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(enum.Array)}].(*catalog.Type)
						members := enum.SQLType.EnumMembers()
						wantTypeACL := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
							objectACLUnitEntry(auth.State.CurrentUser, auth.State.CurrentUser, authorization.UsagePrivilege, 0),
							{Grantor: auth.State.CurrentUser, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
						}}
						if len(members) != 2 || members[0].Label != "first" || members[1].Label != "second" || members[0].ID == members[1].ID || enum.ObjectNamespace != ns || enum.Owner != auth.State.CurrentUser || !enumJournalOf(candidate.Enum).CreatedTypes()[enum.Address()] || array.ObjectNamespace != ns || array.Owner != enum.Owner || !array.SQLType.IsArray() || array.SQLType.Element().ID() != enum.SQLType.ID() || !reflect.DeepEqual(enum.ACL, wantTypeACL) || !reflect.DeepEqual(array.ACL, wantTypeACL) {
							t.Fatal("CREATE ENUM lost actual members/array linkage/ACL/safety bookkeeping")
						}
					}
					var checkpoint *rangePartitionPhaseContext
					if phase == "before-statement-finish" {
						checkpoint = &rangePartitionPhaseContext{Context: message.op.Context(), target: "(*StatementScope).Finish", effect: func() { ownershipManualReplaceDatabase(t, e, session, &before, private) }}
						message.op.ctx = checkpoint
					}
					finishErr := message.scope.Finish(nil)
					if phase == "before-statement-finish" && !checkpoint.entered.Load() {
						t.Fatal("manual utility Finish did not reach actual context barrier")
					}
					if phase == "outer-message" {
						if finishErr != nil || session.transactions.catalog != candidate || e.catalog.state != before.roots.catalog {
							t.Fatal("retained Finish failed early or installed a prefix", finishErr)
						}
						ownershipManualReplaceDatabase(t, e, session, &before, private)
					}
					err := message.end(finishErr)
					if phase != "healthy" {
						var conflict catalog.Conflict
						if !errors.Is(err, ids.ErrCatalogReplaced) && !errors.As(err, &conflict) {
							t.Fatal("missing manual utility DB predecessor veto", err)
						}
						indexMaintenanceUnchanged(t, e, before.roots)
						for address, rows := range before.rows {
							if !reflect.DeepEqual(e.storage.Relations[address].Versions, rows) {
								t.Fatal("veto installed retained row prefix")
							}
						}
						if len(e.storage.Sequences) != len(before.generators) || !reflect.DeepEqual(maps.Collect(e.catalog.state.Names()), maps.Collect(before.roots.catalog.Names())) {
							t.Fatal("veto installed sequence generator/name prefix")
						}
					} else {
						if err != nil || len(e.storage.Relations[roleTableRef(t, e, "ownership_fence_sentinel").Address].Versions) != 2 {
							t.Fatal("healthy utility failed actual publication", err)
						}
						switch utility {
						case "create-sequence":
							if e.catalog.state.Get(created.Address()) != created || e.catalog.state.NameAddress(catalog.DescriptorNameKey(created)) != created.Address() || e.storage.Sequences[created.Address()] != candidate.Generators[created.Address()] {
								t.Fatal("healthy CREATE lost installed descriptor/name/generator")
							}
						case "drop-sequence":
							if e.catalog.state.Get(sequence.Address) != nil || e.catalog.state.NameAddress(catalog.DescriptorNameKey(&old)) != (ids.ObjectAddress{}) {
								t.Fatal("healthy DROP retained sequence descriptor/name")
							}
						case "comment-sequence":
							if e.catalog.state.Get(sequence.Address) != candidate.Descriptors[sequence.Address] {
								t.Fatal("healthy COMMENT lost changed comment descriptor")
							}
						case "create-enum":
							if e.catalog.state.Get(enum.Address()) != enum || e.catalog.state.Get(array.Address()) != array || e.catalog.state.NameAddress(catalog.DescriptorNameKey(enum)) != enum.Address() || e.catalog.state.NameAddress(catalog.DescriptorNameKey(array)) != array.Address() {
								t.Fatal("healthy CREATE ENUM lost enum/array/name installation")
							}
						}
						if private && e.catalog.state.Get(catalog.CurrentDatabaseAddress) != selectedDB {
							t.Fatal("healthy private utility lost selected DB descriptor")
						}
					}
					ownershipManualRetainedUnchanged(t, e, session, sequence, auth, committed, &old, retainedACL, retainedOptions, before, utility == "drop-sequence" && phase == "healthy")
					if auth.Catalog.Get(catalog.CurrentDatabaseAddress) != selectedDB {
						t.Fatal("manual utility followed live DB instead of retained selected root")
					}
				})
			}
		}
	}
}

// The real OWNED BY helper drops Engine.mu to wait for the referenced table,
// then refreshes auth without repeating the earlier namespace CREATE check.
// Losing only that CREATE authority must veto the candidate even though the
// actor still directly owns the table and all later OWNED BY checks remain valid.
func TestRoleOwnershipSequenceOwnedCreationDatabaseFence(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, replace := range []bool{false, true} {
			name := map[bool]string{false: "committed", true: "private"}[private] + "/" + map[bool]string{false: "healthy-wait", true: "replacement-during-owned-wait"}[replace]
			t.Run(name, func(t *testing.T) {
				e, session, sequence, _, committed, changes := ownershipSequenceFenceFixture(t, private)
				ns, table := ownershipManualCreationFixture(t, e, session.authorization.state.CurrentUser)
				// Preserve the same old-comment invariant as the other real routes.
				setup := e.BeginCatalogStage(e.CatalogRead())
				old := *setup.Base.MustRelation(sequence.Address)
				old.Comment, old.ObjectRevision = "retained sequence comment", old.Revision()+1
				if err := setup.Replace(&old); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, setup)
				e.DiscardCatalogStage(setup)
				message, auth, before := ownershipManualStart(t, e, session, sequence, changes)
				selectedDB := auth.Catalog.Get(catalog.CurrentDatabaseAddress)
				if auth.DatabasePredecessor != committed || private && selectedDB == committed || !private && selectedDB != committed {
					t.Fatal("OWNED fixture lacks honest selected/predecessor pair")
				}
				if err := auth.CheckCreate(e.catalog.Domain.Ref(auth.Catalog.Epoch(), ns)); err != nil || auth.Catalog.MustRelation(table.Address).Owner != auth.State.CurrentUser {
					t.Fatal("OWNED fixture lacks DB-derived CREATE and directly owned table", err)
				}
				blockerSession := sessionStart(t, e, nil)
				blocker := batchUnitStart(t, blockerSession, context.Background())
				defer blocker.end(context.Canceled)
				// AcquireCommand/StartStatement alone has not admitted a transaction
				// incarnation. Execute the real table reader before upgrading this
				// same admitted ticket to the blocker AX; do not forge scope.txRef.
				reader, err := blocker.scope.Execute(statement(t, "SELECT * FROM ownership_fence_creation.owned_table"), 0, nil)
				if err != nil {
					t.Fatal("blocker table reader admission failed", err)
				}
				if err := reader.Rows.Close(); err != nil {
					t.Fatal("blocker table reader close failed", err)
				}
				if err := blocker.scope.acquireSequence(table, lock.AccessExclusive, true); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(message.op.Context(), 5*time.Second)
				defer cancel()
				checkpoint := &rangePartitionPhaseContext{Context: ctx, target: "(*StatementScope).acquireSequence"}
				message.op.ctx = checkpoint
				syntax := &parser.CreateSequence{Name: ownershipManualName("ownership_fence_creation", "new_owned_sequence"), Persistence: "logged", Options: []parser.SequenceOption{
					{Name: "start", Value: 71}, {Name: "owned", Owned: ownershipManualName("ownership_fence_creation", "owned_table", "v")},
				}}
				done := make(chan error, 1)
				go func() {
					_, err := message.scope.Execute(syntax, 0, nil)
					done <- err
				}()
				joined := false
				defer func() {
					cancel()
					blocker.end(context.Canceled)
					if !joined {
						select {
						case <-done:
						case <-time.After(5 * time.Second):
							t.Error("OWNED worker did not terminate during cleanup")
						}
					}
				}()
				lockPending(t, e, 1)
				if !checkpoint.entered.Load() {
					t.Fatal("CREATE did not reach actual OWNED BY unlock/admission checkpoint")
				}
				lockState := e.locks.Inspect()
				entry := lockState.Resources[lock.Resource{ObjectRef: table}]
				actualWait := entry != nil && len(entry.Pending) == 1
				if !actualWait {
					t.Fatal("CREATE is not genuinely waiting for the OWNED BY table")
				}
				if replace {
					ownershipManualReplaceDatabase(t, e, session, &before, private)
				}
				blocker.end(context.Canceled)
				select {
				case err := <-done:
					joined = true
					if err != nil {
						t.Fatal("otherwise-valid OWNED helper failed instead of staging candidate", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("OWNED CREATE did not resume after actual table wait")
				}
				fresh, err := operationAuthorization(message.op)
				if err != nil {
					t.Fatal(err)
				}
				if replace && !private && fresh.CheckCreate(e.catalog.Domain.Ref(fresh.Catalog.Epoch(), ns)) == nil {
					t.Fatal("DB-only replacement did not remove earlier schema CREATE authority")
				}
				candidate := message.scope.catalog
				var created *catalog.Relation
				for _, descriptor := range candidate.Descriptors {
					if d, ok := descriptor.(*catalog.Relation); ok && d.Name() == "new_owned_sequence" {
						created = d
					}
				}
				owned := table.Address
				owned.SubID = auth.Catalog.MustRelation(table.Address).Attributes[0]
				if created == nil || created.Owner != auth.State.CurrentUser || created.Namespace() != ns || created.Sequence.Owned != owned || created.Sequence.Start != 71 || candidate.Generators[created.Address()] == nil || candidate.Generators[created.Address()].Called || candidate.Generators[created.Address()].Last != 71 || e.catalog.state != before.roots.catalog {
					t.Fatal("real OWNED helper did not stage otherwise-valid associated sequence without prefix")
				}
				finishErr := message.scope.Finish(nil)
				err = message.end(finishErr)
				if replace {
					var conflict catalog.Conflict
					if !errors.Is(err, ids.ErrCatalogReplaced) && !errors.As(err, &conflict) {
						t.Fatal("missing original pre-OWNED-wait CREATE authority veto", err)
					}
					indexMaintenanceUnchanged(t, e, before.roots)
					if e.catalog.state.NameAddress(catalog.DescriptorNameKey(created)) != (ids.ObjectAddress{}) || e.storage.Sequences[created.Address()] != nil || len(e.storage.Sequences) != len(before.generators) {
						t.Fatal("OWNED veto installed name/generator prefix")
					}
					for address, rows := range before.rows {
						if !reflect.DeepEqual(e.storage.Relations[address].Versions, rows) {
							t.Fatal("OWNED veto installed retained INSERT prefix")
						}
					}
				} else if err != nil || e.catalog.state.Get(created.Address()) != created || e.catalog.state.NameAddress(catalog.DescriptorNameKey(created)) != created.Address() || e.storage.Sequences[created.Address()] != candidate.Generators[created.Address()] || len(e.storage.Relations[roleTableRef(t, e, "ownership_fence_sentinel").Address].Versions) != 2 {
					t.Fatal("healthy actual OWNED wait did not publish association/counter/prefix", err)
				}
				retainedACL := authorization.ACLValue{Explicit: old.Security.ACL.Explicit, Entries: append([]authorization.ACLEntry(nil), old.Security.ACL.Entries...)}
				ownershipManualRetainedUnchanged(t, e, session, sequence, auth, committed, &old, retainedACL, *old.Sequence, before, false)
				if auth.Catalog.Get(catalog.CurrentDatabaseAddress) != selectedDB {
					t.Fatal("OWNED wait mutated retained selected authority")
				}
			})
		}
	}
}
