package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

const captureTriggerLong = "trigger_nullify_next_start_at_on_workspace_autostart_modification"
const captureTriggerName = "trigger_nullify_next_start_at_on_workspace_autostart_modificati"

func triggerCaptureFixture(t *testing.T, partitioned bool) (*core.Engine, *session.Session, []ids.ObjectAddress) {
	t.Helper()
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	queries := []string{
		"CREATE FUNCTION capture_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE TABLE capture_other(id integer,v integer)",
	}
	if partitioned {
		queries = append(queries,
			"CREATE TABLE capture_root(id integer,v integer) PARTITION BY RANGE(id)",
			"CREATE TABLE capture_low PARTITION OF capture_root FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE capture_high PARTITION OF capture_root FOR VALUES FROM(10) TO(20)")
	} else {
		queries = append(queries, "CREATE TABLE capture_root(id integer,v integer)")
	}
	queries = append(queries,
		"CREATE TRIGGER a_retained BEFORE UPDATE ON capture_root FOR EACH ROW EXECUTE FUNCTION capture_fn('kept')",
		"INSERT INTO capture_root VALUES(1,5),(11,7)")
	for _, q := range queries {
		enginetest.SessionSQL(t, s, q)
	}
	addresses := []ids.ObjectAddress{enginetest.RoleTableRef(t, e, "capture_root").Address}
	if partitioned {
		addresses = append(addresses, enginetest.RoleTableRef(t, e, "capture_low").Address, enginetest.RoleTableRef(t, e, "capture_high").Address)
	}
	return e, s, addresses
}

func triggerCaptureRead(e *core.Engine, journal *session.Journal) catalog.Read {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return e.PrivateCatalogLocked(journal)
}

func triggerCaptureParse(t *testing.T, op *session.Operation, query string) (parser.Statement, []sqlstate.Error) {
	t.Helper()
	var notices []sqlstate.Error
	statements, err := parser.ParseWithOptions(query, parser.Options{Context: op.Context(), StandardConformingStrings: true, Notice: func(n sqlstate.Error) {
		notices = append(notices, n)
		op.EmitNotice(session.LevelNotice, n)
	}})
	if err != nil || len(statements) != 1 || len(notices) == 0 {
		t.Fatal("missing actual canonical statement/lexical notice", err, notices)
	}
	for _, n := range notices {
		if n.Code != "42622" || n.Severity != "NOTICE" || n.SeverityUnlocalized != "NOTICE" {
			t.Fatal("wrong lexical diagnostic", n)
		}
	}
	return statements[0], notices
}

func triggerCaptureUnlocked(t *testing.T, e *core.Engine, scope *core.StatementScope) {
	t.Helper()
	for _, lock := range []struct {
		name   string
		try    func() bool
		unlock func()
	}{
		{"engine", e.Mu.TryLock, e.Mu.Unlock},
		{"session", scope.Op.Session.Mu.TryLock, scope.Op.Session.Mu.Unlock},
		{"admission", scope.Owner.Admission.TryLock, scope.Owner.Admission.Unlock},
		{"replacement", e.ReplacementMu.TryLock, e.ReplacementMu.Unlock},
	} {
		if !lock.try() {
			t.Fatalf("notice callback holds %s lock", lock.name)
		}
		lock.unlock()
	}
}

func triggerCaptureSuccessor(t *testing.T, e *core.Engine, scope *core.StatementScope, before catalog.Read, addresses []ids.ObjectAddress, name string, priorOIDs map[ids.ObjectAddress]ids.OID) {
	t.Helper()
	current := triggerCaptureRead(e, scope.PendingCatalog())
	if current.Epoch() != before.Epoch() || scope.LockedRef != before.Domain().Ref(before.Epoch(), addresses[0]) || scope.LockedDescriptor != current.Get(addresses[0]) {
		t.Fatal("active capture did not follow its exact same-epoch successor")
	}
	for _, mutation := range core.ScopeMutations(scope) {
		if mutation.LockedRef.Domain() != nil && mutation.LockedDescriptor != current.Get(mutation.LockedRef.Address) {
			t.Fatal("independent mutation retained predecessor")
		}
	}
	for _, source := range core.InheritanceScopeRelations(&scope.Inheritance) {
		if source.Relation != current.Get(source.Ref.Address) || source.Ref.Domain() != current.Domain() || source.Ref.Epoch != current.Epoch() {
			t.Fatal("independent inheritance capture retained predecessor")
		}
	}
	for address, captured := range scope.PartitionRelations {
		if captured != current.Get(address) {
			t.Fatal("independent topology capture retained predecessor", address)
		}
	}
	for _, address := range addresses {
		old, next := before.MustRelation(address), current.MustRelation(address)
		if old == next || next.Revision() <= old.Revision() || next.Address() != old.Address() || &next.Attributes[0] != &old.Attributes[0] {
			t.Fatal("not an authentic immutable relation successor", old.Name())
		}
		oldDefinitions := stored.TriggersOf(old)
		if len(oldDefinitions) == 0 || oldDefinitions[0].Name() != "a_retained" || oldDefinitions[0].Parts().Arguments[0] != "kept" {
			t.Fatal("retained trigger backing changed")
		}
		definitions := stored.TriggersOf(next)
		if name == "" {
			if len(definitions) != 1 {
				t.Fatal("drop retained canonical trigger")
			}
			continue
		}
		if len(definitions) != 2 || definitions[1].Name() != name {
			t.Fatal("canonical definition missing", old.Name(), definitions)
		}
		parts := definitions[1].Parts()
		if parts.OID == 0 || parts.Relation != current.Domain().Ref(current.Epoch(), address) || parts.Procedure.Domain() != current.Domain() || parts.Procedure.Epoch != current.Epoch() || current.Get(parts.Procedure.Address).Name() != "capture_fn" || current.Get(parts.Procedure.Address) != before.Get(parts.Procedure.Address) {
			t.Fatal("trigger lost exact relation/routine provenance", parts)
		}
		if prior := priorOIDs[address]; prior != 0 && prior != parts.OID {
			t.Fatal("replace/rename changed trigger OID")
		}
		priorOIDs[address] = parts.OID
	}
}

func TestTriggerDDLCanonicalNamePublication(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, partitioned := range []bool{false, true} {
			name := fmt.Sprintf("explicit_%t/partition_%t", explicit, partitioned)
			t.Run(name, func(t *testing.T) {
				e, s, addresses := triggerCaptureFixture(t, partitioned)
				held, description := enginetest.PreparedDescription(t, e, s, "SELECT COALESCE(-(7::integer),0),v FROM capture_root")
				result, err := enginetest.PreparedExecute(e, s, held, &description)
				if err != nil {
					t.Fatal(err)
				}
				result.Rows.Close()
				analysis, accepted := description.Analysis, description.Folds.Current()
				if analysis == nil || len(accepted.Entries) == 0 {
					t.Fatal("missing genuine held backing")
				}
				retainedFold := accepted.Entries[0]
				if explicit {
					enginetest.SessionSQL(t, s, "BEGIN")
				}
				other := enginetest.RoleTableRef(t, e, "capture_other").Address
				otherDescriptor := e.CatalogRead().Get(other)
				oids := make(map[ids.ObjectAddress]ids.OID)
				renamed := strings.Repeat("r", 64)
				for _, action := range []struct {
					query, name string
					notices     int
				}{
					{"CREATE TRIGGER " + captureTriggerLong + " BEFORE INSERT ON capture_root FOR EACH ROW EXECUTE FUNCTION capture_fn('first')", captureTriggerName, 1},
					{"CREATE OR REPLACE TRIGGER " + captureTriggerLong + "_again BEFORE INSERT ON capture_root FOR EACH ROW EXECUTE FUNCTION capture_fn('second')", captureTriggerName, 1},
					{"ALTER TRIGGER " + captureTriggerLong + " ON capture_root RENAME TO " + renamed, renamed[:63], 2},
					{"DROP TRIGGER " + renamed + " ON capture_root", "", 1},
				} {
					before := triggerCaptureRead(e, s.Transactions.Catalog)
					retained := make(map[ids.ObjectAddress]struct{ backing, values []stored.Trigger })
					for _, address := range addresses {
						list := stored.TriggersOf(before.MustRelation(address))
						retained[address] = struct{ backing, values []stored.Trigger }{list, slices.Clone(list)}
					}
					stores := make(map[ids.ObjectAddress]enginetest.HeapAbortSnapshot)
					for _, address := range addresses {
						if store := e.Storage.Relations[address]; store != nil {
							stores[address] = enginetest.CaptureHeapAbort(store)
						}
					}
					op, _, err := s.AcquireCommand(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					scope, err := e.StartStatement(op)
					if err != nil {
						op.Done()
						t.Fatal(err)
					}
					func() {
						defer op.Done()
						defer scope.Finish(errors.New("unfinished capture witness"))
						scope.ProtocolSimpleQuery(1)
						stmt, notices := triggerCaptureParse(t, op, action.query)
						if len(notices) != action.notices {
							t.Fatal("lexical notice count", notices)
						}
						_, err = scope.ExecuteProtocolSimple(stmt, func([]catalog.Column) error { return nil }, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
						if err != nil {
							t.Fatal(action.query, err)
						}
						triggerCaptureSuccessor(t, e, scope, before, addresses, action.name, oids)
						if err = scope.ProtocolNotices(func() error {
							triggerCaptureUnlocked(t, e, scope)
							if got := op.DetachNotices(); !reflect.DeepEqual(got, notices) {
								t.Fatal("notice delivery changed", got, notices)
							}
							return nil
						}); err != nil {
							t.Fatal("authentic successor rejected on notice resume", err)
						}
						if description.Analysis != analysis || description.Folds.Current().Generation != accepted.Generation || &description.Folds.Current().Entries[0] != &accepted.Entries[0] {
							t.Fatal("DDL mutated held accepted backing")
						}
						if err = scope.Finish(nil); err != nil {
							t.Fatal(err)
						}
					}()
					for address, snapshot := range stores {
						enginetest.AssertHeapAbort(t, e.Storage.Relations[address], snapshot, 0)
					}
					for address, snapshot := range retained {
						list := stored.TriggersOf(before.MustRelation(address))
						if len(list) != len(snapshot.backing) || &list[0] != &snapshot.backing[0] || !reflect.DeepEqual(list, snapshot.values) {
							t.Fatal("publication mutated retained predecessor definition backing")
						}
					}
					if e.CatalogRead().Get(other) != otherDescriptor {
						t.Fatal("unrelated relation recaptured/replaced")
					}
				}
				if explicit {
					enginetest.SessionSQL(t, s, "COMMIT")
				}
				result, err = enginetest.PreparedExecute(e, s, held, &description)
				if err != nil {
					t.Fatal("held statement lost executable authority", err)
				}
				result.Rows.Close()
				if !reflect.DeepEqual(accepted.Entries[0], retainedFold) {
					t.Fatal("retained held backing damaged")
				}
			})
		}
	}
	t.Run("notice_rejections", triggerCaptureNoticeRejections)
	t.Run("atomic_capture_transfer", triggerCaptureAtomicTransfer)
}

func triggerCaptureNoticeRejections(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, reason := range []string{"cancel", "source", "foreign", "epoch"} {
			t.Run(fmt.Sprintf("explicit_%t/%s", explicit, reason), func(t *testing.T) {
				e, s, addresses := triggerCaptureFixture(t, false)
				before := e.CatalogRead()
				old := before.MustRelation(addresses[0])
				oldTriggers := stored.TriggersOf(old)
				if explicit {
					enginetest.SessionSQL(t, s, "BEGIN")
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, _, err := s.AcquireCommand(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished notice witness"))
				scope.ProtocolSimpleQuery(1)
				stmt, notices := triggerCaptureParse(t, op, "CREATE TRIGGER "+captureTriggerLong+" BEFORE INSERT ON capture_root FOR EACH ROW EXECUTE FUNCTION capture_fn()")
				if _, err = scope.ExecuteProtocolSimple(stmt, func([]catalog.Column) error { return nil }, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{}); err != nil {
					t.Fatal(err)
				}
				installed := triggerCaptureRead(e, scope.PendingCatalog()).MustRelation(addresses[0])
				if installed == old || scope.LockedDescriptor != installed {
					t.Fatal("missing accepted successor before callback")
				}
				published := e.CatalogRead().Get(addresses[0]) == installed
				var replacement *catalog.Relation
				err = scope.ProtocolNotices(func() error {
					triggerCaptureUnlocked(t, e, scope)
					if got := op.DetachNotices(); !reflect.DeepEqual(got, notices) {
						t.Fatal(got)
					}
					switch reason {
					case "cancel":
						cancel()
					case "source":
						next := *installed
						next.ObjectRevision = installed.Revision() + 1
						next.Comment = "intervening source"
						replacement = &next
						if scope.Catalog != nil {
							e.Mu.Lock()
							e.CatalogChangeLocked(scope.Catalog, next.Address(), &next)
							e.Mu.Unlock()
						} else {
							stage := e.BeginCatalogStage(e.CatalogRead())
							defer e.DiscardCatalogStage(stage)
							if err := stage.Replace(&next); err != nil {
								return err
							}
							enginetest.PublishStage(t, e, stage)
						}
					case "foreign":
						scope.LockedRef = engine.New().Catalog.Domain.Ref(before.Epoch(), addresses[0])
					case "epoch":
						return engine.Reset(context.Background(), e)
					}
					return nil
				})
				want := ids.ErrCatalogReplaced
				if reason == "cancel" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatal("notice resume adopted invalid authority", err)
				}
				if scope.LockedDescriptor != installed {
					t.Fatal("rejected resume recaptured foreign successor")
				}
				_ = scope.Finish(err)
				current := e.CatalogRead()
				if reason == "epoch" {
					if current.Epoch() == before.Epoch() {
						t.Fatal("Reset did not replace epoch")
					}
				} else if !published {
					if current.Get(addresses[0]) != old {
						t.Fatal("failed private command published")
					}
				} else {
					// Late callback rejection does not undo an already committed install.
					expected := installed
					if replacement != nil {
						expected = replacement
					}
					if current.Get(addresses[0]) != expected {
						t.Fatal("late rejection rewound committed publication")
					}
				}
				if before.Get(old.Address()) != old || &stored.TriggersOf(old)[0] != &oldTriggers[0] || oldTriggers[0].Parts().Arguments[0] != "kept" || len(stored.TriggersOf(installed)) != 2 {
					t.Fatal("rejection damaged immutable retained sources")
				}
			})
		}
	}
}
