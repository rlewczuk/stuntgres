package schemacmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/schemacmds"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

// This gate delegates to the genuine catalog provider first. It neither
// substitutes a manifest nor bypasses validation; the gate exposes the real
// outside-lock validation/publication gap under bounded synchronization.
type extensionVersionValidationGate struct {
	catalog.Provider
	engine           *core.Engine
	entered, release chan struct{}
	once             atomic.Bool
}

func (p *extensionVersionValidationGate) Validate(v catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(v, d); err != nil {
		return err
	}
	if ext, ok := d.(*catalog.Extension); ok && ext.Name() == "fuzzystrmatch" && ext.Version == "1.2" && p.once.CompareAndSwap(false, true) {
		if !p.engine.Mu.TryLock() {
			return errors.New("version provider validation holds engine mutex")
		}
		p.engine.Mu.Unlock()
		close(p.entered)
		select {
		case <-p.release:
		case <-time.After(5 * time.Second):
			return errors.New("version validation gate deadline")
		}
	}
	return nil
}

func extensionVersionUnitConsumer(t *testing.T, e *core.Engine, op *session.Operation) ids.ObjectAddress {
	t.Helper()
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "version_consumer"}}}
	call := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "soundex"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "John"}}}}
	create := &parser.CreateTable{Name: name, Elements: []parser.TableElement{{Column: &parser.Column{Name: "v", TypeName: parser.TypeName{Name: "text"}, Constraints: []parser.ColumnConstraint{{Default: call}}}}}}
	if err := extensionUnitExecute(t, e, op, create); err != nil {
		t.Fatal(err)
	}
	// Match the parser's DEFAULT VALUES representation: one empty row,
	// not a missing VALUES fragment.
	if err := extensionUnitExecute(t, e, op, &parser.Insert{Name: name, Rows: [][]parser.Expr{nil}, DefaultValues: true}); err != nil {
		t.Fatal(err)
	}
	return e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "version_consumer"})
}

func TestExtensionVersionPublication(t *testing.T) {
	for _, reason := range []string{"competing", "member_acl", "database_acl", "namespace_acl", "role", "reset", "restore", "cancel"} {
		t.Run(reason, func(t *testing.T) {
			e, admin, old := extensionVersionUnitInstalled(t, "1.1")
			extensionVersionUnitRevoke(t, e, admin)
			consumer := extensionVersionUnitConsumer(t, e, admin)
			oldConsumer, oldStore := e.CatalogRead().Get(consumer), e.Storage.Relations[consumer]
			if oldConsumer == nil || oldStore == nil || len(oldStore.Versions) != 1 {
				t.Fatal("real stored consumer setup")
			}
			data := slices.Clone(oldStore.Versions)
			oldMembers := extensionVersionUnitMembers(t, e.CatalogRead(), old)
			oldAddresses := slices.Clone(old.Members)
			gate := &extensionVersionValidationGate{Provider: e.Catalog.Providers[ids.ExtensionClassID], engine: e, entered: make(chan struct{}), release: make(chan struct{})}
			e.Catalog.Providers[ids.ExtensionClassID] = gate
			var release sync.Once
			unblock := func() { release.Do(func() { close(gate.release) }) }
			worker, closeWorker := enginetest.RoleOperation(t, e, "postgres")
			defer closeWorker()
			ctx, cancel := context.WithCancel(worker.Context())
			defer cancel()
			worker.Ctx = ctx
			type outcome struct {
				err        error
				panicValue any
			}
			done := make(chan outcome, 1)
			go func() {
				result := outcome{}
				defer func() {
					result.panicValue = recover()
					done <- result
				}()
				result.err = extensionUnitExecute(t, e, worker, extensionVersionUnitUpdate())
			}()
			joined := false
			defer func() {
				cancel()
				unblock()
				if !joined {
					select {
					case result := <-done:
						if result.panicValue != nil {
							t.Error("version worker panicked during cleanup", result.panicValue)
						}
					case <-time.After(5 * time.Second):
						t.Error("version worker cleanup deadline")
					}
				}
			}()
			select {
			case <-gate.entered:
			case result := <-done:
				joined = true
				t.Fatal("update did not reach real provider gate", result.err, result.panicValue)
			case <-time.After(5 * time.Second):
				t.Fatal("real version validation did not arrive")
			}
			if e.CatalogRead().Get(old.Address()) != old || old.Version != "1.1" || !reflect.DeepEqual(old.Members, oldAddresses) {
				t.Fatal("staged update leaked")
			}
			switch reason {
			case "competing":
				if err := extensionUnitExecute(t, e, admin, extensionVersionUnitUpdate()); err != nil {
					t.Fatal("genuine peer update", err)
				}
			case "member_acl":
				grant := &parser.ObjectGrant{Kind: "function", Privileges: []parser.GrantPrivilege{{Name: "execute"}}, Grantees: []parser.GrantGrantee{{Public: true}}, Routines: []parser.GrantRoutineTarget{{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "public"}, {Text: "levenshtein"}}}, SignatureSpecified: true, Arguments: []parser.GrantRoutineArgument{{Type: parser.TypeName{Name: "text"}}, {Type: parser.TypeName{Name: "text"}}}}}}
				if err := extensionUnitExecute(t, e, admin, grant); err != nil {
					t.Fatal(err)
				}
			case "database_acl":
				grant := extensionUnitGrant(e.CatalogRead().MustDatabase(catalog.CurrentDatabaseAddress).Name(), "")
				grant.Grantees = []parser.GrantGrantee{{Public: true}}
				if err := extensionUnitExecute(t, e, admin, grant); err != nil {
					t.Fatal(err)
				}
			case "namespace_acl":
				grant := &parser.ObjectGrant{Kind: "schema", Revoke: true, Privileges: []parser.GrantPrivilege{{Name: "usage"}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "public"}}}}, Grantees: []parser.GrantGrantee{{Public: true}}}
				if err := extensionUnitExecute(t, e, admin, grant); err != nil {
					t.Fatal(err)
				}
			case "role":
				roleAttributeCreate(t, e, admin, "version_role_fence", false, parser.RoleOptions{})
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			case "restore":
				replacement := e.RestoreStage()
				if _, err := e.ReplaceCatalog(context.Background(), replacement, replacement.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			winner := e.CatalogRead()
			winnerMembers := map[ids.ObjectAddress]extensionVersionMemberSnapshot{}
			if ext, ok := winner.Extension(old.Address()); ok {
				winnerMembers = extensionVersionUnitMembers(t, winner, ext)
			}
			unblock()
			var failure error
			select {
			case result := <-done:
				joined = true
				if result.panicValue != nil {
					t.Fatal("version worker panicked", result.panicValue)
				}
				failure = result.err
			case <-time.After(5 * time.Second):
				t.Fatal("version worker join deadline")
			}
			if failure == nil {
				t.Fatal("stale version update published", reason)
			}
			if reason == "competing" || reason == "member_acl" {
				var conflict catalog.Conflict
				var state interface{ SQLState() string }
				if !errors.As(failure, &conflict) || errors.As(failure, &state) {
					t.Fatal("publication conflict must remain ordinary", failure)
				}
			}
			if reason == "cancel" && !errors.Is(failure, context.Canceled) {
				t.Fatal("canceled update", failure)
			}
			if e.CatalogRead().State() != winner.State() {
				t.Fatal("loser replaced winner catalog")
			}
			extensionVersionUnitUnchanged(t, e.CatalogRead(), winnerMembers)
			if old.Version != "1.1" || !reflect.DeepEqual(old.Members, oldAddresses) {
				t.Fatal("loser mutated retained extension")
			}
			for _, prior := range oldMembers {
				if !reflect.DeepEqual(*prior.procedure, prior.copy) || !authorization.EqualACL(prior.procedure.ACL, prior.acl) {
					t.Fatal("loser mutated retained member or ACL")
				}
			}
			if reason != "reset" && reason != "restore" {
				if e.CatalogRead().Get(consumer) != oldConsumer || e.Storage.Relations[consumer] != oldStore || !reflect.DeepEqual(data, oldStore.Versions) {
					t.Fatal("update changed consumer or data")
				}
				if reason == "competing" {
					ext := e.CatalogRead().MustExtension(old.Address())
					if ext.Version != "1.2" || len(ext.Members) != 10 {
						t.Fatal("winner version manifest")
					}
					extensionVersionUnitUnchanged(t, e.CatalogRead(), oldMembers)
				} else if e.CatalogRead().MustExtension(old.Address()).Version != "1.1" {
					t.Fatal("failed update changed installed version")
				}
			}
		})
	}
	t.Run("CanceledBeforeValidation", func(t *testing.T) {
		e, op, ext := extensionVersionUnitInstalled(t, "1.1")
		members := extensionVersionUnitMembers(t, e.CatalogRead(), ext)
		e.Mu.Lock()
		stage, err := schemacmds.StageExtensionUpdateLocked(e, &core.StatementScope{Op: op}, extensionVersionUnitUpdate())
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		defer e.DiscardCatalogStage(stage)
		before := e.CatalogRead().State()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.ValidateCatalogStage(ctx, stage); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if e.CatalogRead().State() != before || ext.Version != "1.1" {
			t.Fatal("canceled stage published")
		}
		extensionVersionUnitUnchanged(t, e.CatalogRead(), members)
	})
}

// The competing update can commit after transfer to the statement journal,
// not only while the provider callback is running. Preparation must reject
// the captured predecessor before validating an overlaid, mixed-version graph.
func TestExtensionVersionPublicationAfterTransfer(t *testing.T) {
	e, admin, old := extensionVersionUnitInstalled(t, "1.1")
	worker, closeWorker := enginetest.RoleOperation(t, e, "postgres")
	defer closeWorker()
	scope, err := e.StartStatement(worker)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = scope.Finish(errors.New("unfinished version transfer"))
		}
	}()
	if _, err := scope.Execute(extensionVersionUnitUpdate(), 0, nil); err != nil {
		t.Fatal("private version transfer", err)
	}
	if scope.Catalog == nil || scope.Catalog.Originals[old.Address()] != old || e.CatalogRead().MustExtension(old.Address()) != old {
		t.Fatal("transfer lost committed predecessor or published prematurely")
	}
	if err := extensionUnitExecute(t, e, admin, extensionVersionUnitUpdate()); err != nil {
		t.Fatal("competing committed version", err)
	}
	winner := e.CatalogRead()
	members := extensionVersionUnitMembers(t, winner, winner.MustExtension(old.Address()))
	finished = true
	err = scope.Finish(nil)
	var conflict catalog.Conflict
	var state interface{ SQLState() string }
	if !errors.As(err, &conflict) || errors.As(err, &state) {
		t.Fatal("stale journal must fail before mixed-graph validation", err)
	}
	if e.CatalogRead().State() != winner.State() || old.Version != "1.1" {
		t.Fatal("failed journal changed winner or retained predecessor")
	}
	extensionVersionUnitUnchanged(t, e.CatalogRead(), members)
}

// Genuine SQL stages reuse private predecessors without turning them into
// committed originals. Rollback must discard the complete membership/rename/
// relocation chain, including the original absence and vacant-name fences.
func TestExtensionPrivateJournalPublication(t *testing.T) {
	for _, finish := range []string{"ROLLBACK", "COMMIT"} {
		t.Run(finish, func(t *testing.T) {
			e, op, ext := extensionUnitInstalled(t)
			enginetest.RoleExecute(t, e, op, "CREATE SCHEMA private_extension_target")
			base := e.CatalogRead()
			oldMembers := slices.Clone(ext.Members)
			enginetest.RoleExecute(t, e, op, "BEGIN")
			enginetest.RoleExecute(t, e, op, "CREATE FUNCTION public.private_extension_member(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'")
			key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ext.TargetNamespace, Name: "private_extension_member", Signature: "23"}
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(op.Session.Transactions.Catalog)
			address := private.NameAddress(key)
			e.Mu.Unlock()
			if address.ObjectID == 0 {
				t.Fatal("missing genuine private function")
			}
			for _, query := range []string{
				"ALTER EXTENSION unaccent ADD FUNCTION public.private_extension_member(integer)",
				"ALTER EXTENSION unaccent DROP FUNCTION public.private_extension_member(integer)",
				"ALTER FUNCTION public.private_extension_member(integer) RENAME TO private_extension_renamed",
				"ALTER EXTENSION unaccent ADD FUNCTION public.private_extension_renamed(integer)",
				"ALTER EXTENSION unaccent SET SCHEMA private_extension_target",
				"ALTER EXTENSION unaccent DROP FUNCTION private_extension_target.private_extension_renamed(integer)",
			} {
				enginetest.RoleExecute(t, e, op, query)
				journal := op.Session.Transactions.Catalog
				original, observed := journal.Originals[address]
				if !observed || original != nil || journal.Originals[ext.Address()] != ext {
					t.Fatal("private predecessor replaced committed original", query)
				}
				if originalName, observed := journal.Names[key]; !observed || originalName != (ids.ObjectAddress{}) {
					t.Fatal("private rename replaced original name absence", query)
				}
				e.Mu.Lock()
				private = e.PrivateCatalogLocked(journal)
				err := e.CatalogPreflightLocked(journal)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal("lawful private predecessor conflicted", query, err)
				}
				if err := catalog.ValidateExtensionMembership(private, private.MustExtension(ext.Address())); err != nil {
					t.Fatal("private reciprocal inventory", query, err)
				}
				if e.CatalogRead().State() != base.State() || !slices.Equal(ext.Members, oldMembers) {
					t.Fatal("private change leaked into committed/retained state", query)
				}
			}
			if catalog.ExtensionMembership(private.MustProcedure(address)) != (ids.ObjectAddress{}) || len(private.MustExtension(ext.Address()).Members) != len(oldMembers) {
				t.Fatal("final detach retained a reciprocal member")
			}
			enginetest.RoleExecute(t, e, op, finish)
			published := e.CatalogRead()
			if finish == "ROLLBACK" {
				if published.State() != base.State() || published.Get(address) != nil || published.NameAddress(key) != (ids.ObjectAddress{}) {
					t.Fatal("rollback leaked private identity or names")
				}
			} else {
				final := private.MustProcedure(address)
				if published.Get(address) != final || published.NameAddress(catalog.DescriptorNameKey(final)) != address || published.NameAddress(key) != (ids.ObjectAddress{}) ||
					published.MustExtension(ext.Address()).TargetNamespace != final.Namespace() || len(published.MustExtension(ext.Address()).Members) != len(oldMembers) {
					t.Fatal("commit lost detached identity, final name or relocation")
				}
				if err := catalog.ValidateExtensionMembership(published, published.MustExtension(ext.Address())); err != nil {
					t.Fatal("committed reciprocal inventory", err)
				}
			}
			if base.Get(address) != nil || base.MustExtension(ext.Address()) != ext || !slices.Equal(ext.Members, oldMembers) {
				t.Fatal("finalization mutated retained committed state")
			}
		})
	}
}

// A schema member has one e edge, not recursive membership in its ordinary
// children. Creating/dropping a child between membership operations must not
// leave the schema's private predecessor attached after DROP membership.
func TestExtensionPrivateSchemaMembership(t *testing.T) {
	e, op, ext := extensionUnitInstalled(t)
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA private_schema_member")
	base := e.CatalogRead()
	schema := base.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "private_schema_member"})
	enginetest.RoleExecute(t, e, op, "BEGIN")
	for _, query := range []string{
		"ALTER EXTENSION unaccent ADD SCHEMA private_schema_member",
		"CREATE TABLE private_schema_member.witness(v integer)",
		"DROP TABLE private_schema_member.witness",
		"ALTER EXTENSION unaccent DROP SCHEMA private_schema_member",
	} {
		enginetest.RoleExecute(t, e, op, query)
		e.Mu.Lock()
		private := e.PrivateCatalogLocked(op.Session.Transactions.Catalog)
		e.Mu.Unlock()
		current := private.MustExtension(ext.Address())
		if err := catalog.ValidateExtensionMembership(private, current); err != nil {
			t.Fatal("schema reciprocal inventory", query, err)
		}
		attached := query != "ALTER EXTENSION unaccent DROP SCHEMA private_schema_member"
		want := len(ext.Members)
		if attached {
			want++
		}
		if len(current.Members) != want || slices.Contains(current.Members, schema) != attached ||
			(catalog.ExtensionMembership(private.Get(schema)) == ext.Address()) != attached {
			t.Fatal("schema/child operations changed actual membership", query, current.Members)
		}
		for _, address := range current.Members {
			if address != schema && !slices.Contains(ext.Members, address) {
				t.Fatal("schema child became a direct member", query, address)
			}
		}
	}
	enginetest.RoleExecute(t, e, op, "ROLLBACK")
	if e.CatalogRead().State() != base.State() || catalog.ExtensionMembership(base.Get(schema)) != (ids.ObjectAddress{}) {
		t.Fatal("schema rollback changed committed membership")
	}
}
