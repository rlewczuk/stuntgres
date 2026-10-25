package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This gate delegates to the genuine catalog provider first. It neither
// substitutes a manifest nor bypasses validation; the gate exposes the real
// outside-lock validation/publication gap under bounded synchronization.
type extensionVersionValidationGate struct {
	catalog.Provider
	engine           *Engine
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

func extensionVersionUnitConsumer(t *testing.T, e *Engine, op *session.Operation) ids.ObjectAddress {
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
			worker, closeWorker := roleOperation(t, e, "postgres")
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
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "restore":
				replacement := e.RestoreStage()
				if _, err := e.ReplaceCatalogInOperation(context.Background(), replacement, replacement.CatalogRead(), admin); err != nil {
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
		stage, err := e.stageExtensionUpdateLocked(&StatementScope{Op: op}, extensionVersionUnitUpdate())
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
