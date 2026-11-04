package tablecmds_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/sepgsql"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func sepgsqlSequenceHold(t *testing.T, e *core.Engine, s *session.Session, resource lock.Resource) ids.TxRef {
	t.Helper()
	tx := enginetest.TransactionBegin(t, s)
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	// StartStatement owns admission; Gate attaches the existing transaction
	// incarnation and its genuine catalog-lock ticket to this statement.
	if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
		t.Fatal(err)
	}
	if scope.TxRef != tx || scope.LockTicket == nil {
		t.Fatal("namespace blocker lost its original transaction ticket")
	}
	err = scope.AcquireCatalogResource(resource, lock.AccessExclusive)
	if err == nil && !e.Locks.Holds(scope.LockTicket, resource, lock.AccessExclusive) {
		t.Fatal("namespace blocker did not hold its selected catalog resource", resource)
	}
	if err = scope.Finish(err); err != nil {
		t.Fatal(err)
	}
	return tx
}

func sepgsqlSequencePending(t *testing.T, e *core.Engine, worker *session.Session, resource lock.Resource, mode lock.Mode) {
	t.Helper()
	enginetest.LockPending(t, e, 1)
	entry := e.Locks.Inspect().Resources[resource]
	if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Mode != mode ||
		entry.Pending[0].Ticket.Ref().Session() != worker.SessionToken || entry.Pending[0].Terminal {
		t.Fatal("missing authentic selected sequence admission", resource, entry)
	}
}

func TestSepgsqlSimulationSequenceCreateCurrentness(t *testing.T) {
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	t.Run("concurrent_binding_skip_or_duplicate_has_no_loser_hook", func(t *testing.T) {
		for _, conditional := range []bool{false, true} {
			t.Run(map[bool]string{false: "duplicate", true: "skip"}[conditional], func(t *testing.T) {
				e, events := sepgsqlSequenceEngine(t, nil)
				a, b := sepgsqlSequenceActor(t, e), sepgsqlSequenceActor(t, e)
				tx := enginetest.TransactionBegin(t, a)
				ended := false
				t.Cleanup(func() {
					if !ended {
						enginetest.TransactionEnd(t, a, tx, false)
					}
				})
				enginetest.SessionSQL(t, a, "CREATE SEQUENCE winner START 5 RESTART 9 CACHE 3")
				q := "CREATE SEQUENCE winner OWNED BY absent.c"
				if conditional {
					q = "CREATE SEQUENCE IF NOT EXISTS winner OWNED BY absent.c"
				}
				ast, description := sequencePreparedDescription(t, e, b, q)
				ctx, cancel := context.WithCancel(t.Context())
				op, err := b.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				joined := false
				t.Cleanup(func() {
					cancel()
					if !joined {
						<-done
					}
					op.Done()
				})
				go func() {
					scope, err := e.StartStatement(op)
					if err == nil {
						scope.Expected = &description
						_, err = scope.Execute(ast, 0, nil)
						err = scope.Finish(err)
					}
					done <- err
				}()
				key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: public, Name: "winner"}
				resource := lock.Resource{ObjectRef: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), public), Name: key}
				sepgsqlSequencePending(t, e, b, resource, lock.AccessExclusive)
				if len(sepgsqlSequenceEvents(events())) != 1 {
					t.Fatal("loser ran postcreate before genuine binding admission")
				}
				enginetest.TransactionEnd(t, a, tx, true)
				ended = true
				winner := e.CatalogRead()
				address := winner.NameAddress(key)
				generator := *e.Storage.Sequences[address]
				select {
				case err = <-done:
					joined = true
				case <-time.After(5 * time.Second):
					t.Fatal("binding loser failed to drain")
				}
				if conditional {
					want := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "winner" already exists, skipping`}}
					if err != nil || !reflect.DeepEqual(op.DetachNotices(), want) {
						t.Fatal("concurrent IF NOT EXISTS diagnostic", err)
					}
				} else {
					var got *sqlstate.Error
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: `duplicate key value violates unique constraint "pg_class_relname_nsp_index"`, Detail: fmt.Sprintf("Key (relname, relnamespace)=(winner, %d) already exists.", public.ObjectID), Schema: "pg_catalog", Table: "pg_class", Constraint: "pg_class_relname_nsp_index"}
					if !errors.As(err, &got) || *got != want {
						t.Fatalf("concurrent duplicate %#v, want %#v", got, want)
					}
				}
				op.Done()
				if e.CatalogRead().State() != winner.State() || *e.Storage.Sequences[address] != generator || len(sepgsqlSequenceEvents(events())) != 1 {
					t.Fatal("loser rewrote winner label/generator or emitted sequence CREATE")
				}
				sepgsqlSequenceLabel(t, winner, address, "P")
				enginetest.LockEmpty(t, e)
			})
		}
	})
	for _, change := range []string{"descriptor", "label", "role_root", "cancel", "epoch"} {
		t.Run("namespace_wait_"+change, func(t *testing.T) {
			e, events := sepgsqlSequenceEngine(t, nil)
			admin := enginetest.StartSession(t, e, nil)
			worker := sepgsqlSequenceActor(t, e)
			enginetest.SessionSQL(t, admin, "CREATE SCHEMA chosen")
			namespace := schemaRef(t, e, "chosen")
			resource := lock.Resource{ObjectRef: namespace}
			tx := sepgsqlSequenceHold(t, e, admin, resource)
			ended := false
			t.Cleanup(func() {
				if !ended {
					enginetest.TransactionEnd(t, admin, tx, false)
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			await := enginetest.ExecuteAsync(t, e, worker, ctx, "CREATE SEQUENCE chosen.late OWNED BY absent.c")
			sepgsqlSequencePending(t, e, worker, resource, lock.AccessShare)
			if len(sepgsqlSequenceEvents(events())) != 0 {
				t.Fatal("namespace admission must precede postcreate")
			}
			switch change {
			case "descriptor":
				enginetest.SessionSQL(t, admin, "ALTER SCHEMA chosen RENAME TO replacement")
			case "label":
				enginetest.SessionSQL(t, admin, "SECURITY LABEL ON SCHEMA chosen IS 'Q'")
			case "role_root":
				enginetest.SessionSQL(t, admin, "CREATE USER unrelated_role")
			case "cancel":
				cancel()
			case "epoch":
				// Real replacement fences the still-pending request and its
				// captured epoch, rather than substituting a cancellation.
				if err := engine.Reset(t.Context(), e); err != nil {
					t.Fatal(err)
				}
				controlsRequire(t, admin, "ROLLBACK")
				ended = true
			}
			if !ended {
				enginetest.TransactionEnd(t, admin, tx, change != "cancel")
				ended = true
			}
			stable := e.CatalogRead()
			_, err := await()
			if change == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("lost external cancellation cause", err)
				}
			} else if change == "epoch" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("lost actual replacement epoch", err)
				}
			} else if err == nil || err.Error() != (catalog.Conflict{}).Error() {
				t.Fatal("replaced namespace predecessor accepted", err)
			}
			if e.CatalogRead().State() != stable.State() || len(sepgsqlSequenceEvents(events())) != 0 {
				t.Fatal("stale namespace created sequence or ran MAC")
			}
			sepgsqlSequenceAbsent(t, e, namespace.Address, "late")
			enginetest.LockEmpty(t, e)
		})
	}
	for _, change := range []string{"label", "schema_descriptor", "create_revocation", "role_root", "cancel", "epoch"} {
		t.Run("owned_by_wait_"+change, func(t *testing.T) {
			e, events := sepgsqlSequenceEngine(t, func(c *sepgsql.SimulationConfig) {
				c.Policy.NamedSpecs = map[string]string{"namespace-change": "db_schema *.chosen Q"}
			})
			admin := enginetest.StartSession(t, e, nil)
			worker := sepgsqlSequenceActor(t, e)
			for _, q := range []string{"CREATE USER actor", "CREATE SCHEMA chosen", "GRANT USAGE, CREATE ON SCHEMA chosen TO actor", "CREATE TABLE chosen.parent(c integer)", "ALTER TABLE chosen.parent OWNER TO actor"} {
				enginetest.SessionSQL(t, admin, q)
			}
			enginetest.SessionSQL(t, worker, "SET SESSION AUTHORIZATION actor")
			namespace := schemaRef(t, e, "chosen")
			parent := enginetest.RoleTableRef(t, e, "chosen.parent")
			tx := enginetest.TransactionBegin(t, admin)
			enginetest.SessionSQL(t, admin, "LOCK TABLE chosen.parent IN ACCESS EXCLUSIVE MODE")
			ended := false
			t.Cleanup(func() {
				if !ended {
					enginetest.TransactionEnd(t, admin, tx, false)
				}
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			await := enginetest.ExecuteAsync(t, e, worker, ctx, "CREATE SEQUENCE chosen.late OWNED BY chosen.parent.c")
			sepgsqlSequencePending(t, e, worker, lock.Resource{ObjectRef: parent}, lock.AccessShare)
			// The queued OWNED BY lock is authentic after successful MAC, not a
			// paused fake policy. Audit delivery occurs on the worker's Done.
			switch change {
			case "label":
				peer := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, peer, "SELECT sepgsql_restorecon('namespace-change')")
				key := catalog.SecurityLabelKey{Object: namespace.Address, Provider: "selinux"}
				if record := e.CatalogRead().State().SecurityLabel(key); record == nil || record.Label != "Q" {
					t.Fatal("genuine restorecon did not replace the committed namespace label", record)
				}
			case "schema_descriptor":
				enginetest.SessionSQL(t, admin, "COMMENT ON SCHEMA chosen IS 'changed'")
			case "create_revocation":
				enginetest.SessionSQL(t, admin, "REVOKE CREATE ON SCHEMA chosen FROM actor")
			case "role_root":
				enginetest.SessionSQL(t, admin, "ALTER ROLE actor NOINHERIT")
			case "cancel":
				cancel()
			case "epoch":
				if err := engine.Reset(t.Context(), e); err != nil {
					t.Fatal(err)
				}
				controlsRequire(t, admin, "ROLLBACK")
				ended = true
			}
			if !ended {
				enginetest.TransactionEnd(t, admin, tx, change != "cancel")
				ended = true
			}
			stable, roles := e.CatalogRead(), e.RoleStore.Published
			_, err := await()
			if change == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("lost external OWNED BY cancellation", err)
				}
			} else if change == "epoch" {
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("OWNED BY adopted replacement epoch", err)
				}
			} else if err == nil || err.Error() != (catalog.Conflict{}).Error() {
				t.Fatal("earlier CREATE authority silently refreshed", err)
			}
			if e.CatalogRead().State() != stable.State() || e.RoleStore.Published != roles {
				t.Fatal("stale OWNED BY published sequence/label/generator")
			}
			sepgsqlSequenceAbsent(t, e, namespace.Address, "late")
			creates := sepgsqlSequenceEvents(events())
			if len(creates) != 1 || creates[0].Subject != "R" || creates[0].Target != "P" || len(creates[0].Denied) != 0 {
				t.Fatal("authentic successful postcreate evidence lost/replayed", creates)
			}
			enginetest.LockEmpty(t, e)
		})
	}
	t.Run("current_subject_not_sql_role_or_previous_phase", func(t *testing.T) {
		e, events := sepgsqlSequenceEngine(t, func(c *sepgsql.SimulationConfig) {
			c.Policy.TrustedTransitions = []sepgsql.TrustedTransition{{Source: "R", Procedure: "Q", Result: "T"}}
			c.Policy.Rules = append(c.Policy.Rules, sepgsql.Rule{Source: "R", Target: "Q", Class: "db_procedure", Allow: []string{"execute", "entrypoint"}})
		})
		admin := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, admin, "CREATE FUNCTION sequence_trusted() RETURNS text LANGUAGE SQL AS 'SELECT sepgsql_getcon()'")
		enginetest.SessionSQL(t, admin, "SECURITY LABEL ON FUNCTION sequence_trusted() IS 'Q'")
		s := sepgsqlSequenceActor(t, e)
		ast, description := sequencePreparedDescription(t, e, s, "CREATE SEQUENCE late")
		result := enginetest.SessionSQL(t, s, "SELECT sequence_trusted()")
		rows := enginetest.Tuples(result)
		result.Rows.Close()
		if len(rows) != 1 || rows[0][0] != "T" {
			t.Fatal("actual trusted routine did not enter T", rows)
		}
		if _, err := sequencePreparedExecute(e, s, ast, &description); err != nil {
			t.Fatal("balanced trusted exit did not restore R", err)
		}
		enginetest.SessionSQL(t, admin, "DROP SEQUENCE late")
		enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('T')")
		_, err := sequencePreparedExecute(e, s, ast, &description)
		sequenceOwnerDiagnostic(t, err, "42501", "SELinux: security policy violation", "")
		creates := sepgsqlSequenceEvents(events())
		if len(creates) != 2 || creates[0].Subject != "R" || len(creates[0].Denied) != 0 ||
			creates[1].Subject != "T" || !slices.Equal(creates[1].Denied, []string{"create"}) {
			t.Fatal("prepared handle reused previous subject", creates)
		}
		sepgsqlSequenceAbsent(t, e, public, "late")
	})
	t.Run("own_private_namespace_and_committed_predecessor", func(t *testing.T) {
		e, _ := sepgsqlSequenceEngine(t, func(c *sepgsql.SimulationConfig) {
			c.Policy.Rules = append(c.Policy.Rules, sepgsql.Rule{Source: "R", Target: "Q", Class: "db_schema", Allow: []string{"search", "add_name"}})
		})
		s := enginetest.StartSession(t, e, nil)
		before := e.CatalogRead()
		enginetest.SessionSQL(t, s, "BEGIN")
		enginetest.SessionSQL(t, s, "CREATE SCHEMA own_private")
		enginetest.SessionSQL(t, s, "SECURITY LABEL ON SCHEMA own_private IS 'Q'")
		enginetest.SessionSQL(t, s, "SELECT sepgsql_setcon('R')")
		enginetest.SessionSQL(t, s, "CREATE SEQUENCE own_private.child")
		journal := s.Transactions.Catalog
		e.Mu.Lock()
		private := e.PrivateCatalogLocked(journal)
		e.Mu.Unlock()
		namespace := private.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "own_private"})
		address := private.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "child"})
		key := catalog.SecurityLabelKey{Object: namespace, Provider: "selinux"}
		observed, present := journal.LabelReads[key]
		if namespace.ObjectID == 0 || !present || observed != nil || journal.Originals[namespace] != nil ||
			journal.LabelOriginals[key] != nil || private.State().SecurityLabel(key).Label != "Q" ||
			e.CatalogRead().State() != before.State() {
			t.Fatal("private parent was substituted for its committed absent predecessor", journal)
		}
		sepgsqlSequenceLabel(t, private, address, "Q")
		enginetest.SessionSQL(t, s, "COMMIT")
		sepgsqlSequenceLabel(t, e.CatalogRead(), address, "Q")
	})
}
