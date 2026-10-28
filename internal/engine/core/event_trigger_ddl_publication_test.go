package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestEventTriggerDDLPublication(t *testing.T) {
	t.Run("original_identity_admission", eventTriggerDDLPublicationAdmission)
	t.Run("authority_and_replacement", eventTriggerDDLPublicationFences)
	t.Run("depth_only_routine", eventTriggerDDLPublicationRoutineDepth)
	for _, action := range []string{"ENABLE REPLICA", "ENABLE ALWAYS", "DISABLE", "RENAME TO et_pub_new", "OWNER TO et_pub_owner", "OWNER TO CURRENT_USER", "DROP"} {
		for _, finish := range []string{"success", "veto", "cancel"} {
			t.Run(action+"/"+finish, func(t *testing.T) {
				e := engine.New()
				actor := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE ROLE et_pub_owner SUPERUSER", "CREATE FUNCTION et_pub_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$", "CREATE EVENT TRIGGER et_pub ON sql_drop WHEN TAG IN ('DROP TABLE','DROP TABLE') EXECUTE FUNCTION et_pub_fn()"} {
					enginetest.SessionSQL(t, actor, q)
				}
				before := e.CatalogRead()
				key := ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "et_pub"}
				address := before.NameAddress(key)
				original := before.Get(address).(*catalog.EventTrigger)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				op, err := actor.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished publication witness"))
				// A genuine enclosing command owns delayed Finish; standalone utilities
				// retain their canonical immediate CompleteUtility publication.
				previous, err := scope.BeginEventCommand("DROP TABLE")
				if err != nil {
					t.Fatal(err)
				}
				q := "ALTER EVENT TRIGGER et_pub " + action
				if action == "DROP" {
					q = "DROP EVENT TRIGGER et_pub"
				}
				if _, err = scope.Execute(enginetest.Statement(t, q), 0, nil); err != nil {
					t.Fatal(q, err)
				}
				if err = scope.FinishEventCommand(previous, nil); err != nil {
					t.Fatal(err)
				}
				if e.CatalogRead().State() != before.State() || before.Get(address) != original || before.NameAddress(key) != address || original.FiringMode != catalog.TriggerOrigin || original.Name() != "et_pub" {
					t.Fatal("published before Finish or mutated predecessor")
				}
				private := e.PrivateCatalogLocked(scope.PendingCatalog())
				next, _ := private.Get(address).(*catalog.EventTrigger)
				switch action {
				case "DROP":
					if next != nil || private.NameAddress(key) != (ids.ObjectAddress{}) {
						t.Fatal("drop missing privately")
					}
				case "OWNER TO CURRENT_USER":
					if next != original || next.Revision() != original.Revision() {
						t.Fatal("same-owner allocated replacement")
					}
				default:
					if next == nil || next == original || next.Address() != address || next.Revision() <= original.Revision() || next.Function != original.Function || next.Event != original.Event || !reflect.DeepEqual(next.Tags, original.Tags) {
						t.Fatal("invalid immutable successor", next)
					}
					switch action {
					case "ENABLE REPLICA":
						if next.FiringMode != catalog.TriggerReplica {
							t.Fatal(next)
						}
					case "ENABLE ALWAYS":
						if next.FiringMode != catalog.TriggerAlways {
							t.Fatal(next)
						}
					case "DISABLE":
						if next.FiringMode != catalog.TriggerDisabled {
							t.Fatal(next)
						}
					case "RENAME TO et_pub_new":
						if next.Name() != "et_pub_new" || private.NameAddress(key) != (ids.ObjectAddress{}) || private.NameAddress(ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "et_pub_new"}) != address {
							t.Fatal("rename binding")
						}
					case "OWNER TO et_pub_owner":
						if next.Owner == original.Owner {
							t.Fatal("owner unchanged")
						}
					}
				}
				veto := errors.New("finish veto")
				var primary error
				if finish == "veto" {
					primary = veto
				}
				if finish == "cancel" {
					cancel()
				}
				err = scope.Finish(primary)
				if finish == "success" {
					installed := e.CatalogRead()
					if err != nil {
						t.Fatal("success rejected", err)
					}
					if action == "DROP" {
						if installed.Get(address) != nil || installed.NameAddress(key) != (ids.ObjectAddress{}) {
							t.Fatal("successful DROP retained descriptor or binding")
						}
						expectEventIndex(t, installed, 0)
					} else if installed.Get(address) != next {
						t.Fatal("success did not install exact successor")
					}
				} else {
					want := veto
					if finish == "cancel" {
						want = context.Canceled
					}
					if !errors.Is(err, want) || e.CatalogRead().State() != before.State() {
						t.Fatal("rejected Finish changed root", err)
					}
				}
				if before.Get(address) != original || original.Name() != "et_pub" || original.Owner != before.Get(address).(*catalog.EventTrigger).Owner || original.FiringMode != catalog.TriggerOrigin || len(original.Tags) != 2 {
					t.Fatal("retained root mutated")
				}
				expectEventIndex(t, before, 1)
			})
		}
	}
}

func eventTriggerDDLPublicationAdmission(t *testing.T) {
	for _, noOp := range []bool{false, true} {
		for _, change := range []string{"rename", "drop", "recreate"} {
			t.Run(change+map[bool]string{false: "/state", true: "/same_owner"}[noOp], func(t *testing.T) {
				e := engine.New()
				owner := enginetest.StartSession(t, e, nil)
				waiter := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE FUNCTION et_admit_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$", "CREATE EVENT TRIGGER et_admit ON ddl_command_end EXECUTE FUNCTION et_admit_fn()"} {
					enginetest.SessionSQL(t, owner, q)
				}
				before := e.CatalogRead()
				key := ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "et_admit"}
				address := before.NameAddress(key)
				enginetest.SessionSQL(t, owner, "BEGIN")
				defer enginetest.SessionSQL(t, owner, "ROLLBACK")
				if change == "rename" {
					enginetest.SessionSQL(t, owner, "ALTER EVENT TRIGGER et_admit RENAME TO et_admit_new")
				} else {
					enginetest.SessionSQL(t, owner, "DROP EVENT TRIGGER et_admit")
					if change == "recreate" {
						enginetest.SessionSQL(t, owner, "CREATE EVENT TRIGGER et_admit ON ddl_command_end EXECUTE FUNCTION et_admit_fn()")
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				q := "ALTER EVENT TRIGGER et_admit ENABLE REPLICA"
				if noOp {
					q = "ALTER EVENT TRIGGER et_admit OWNER TO CURRENT_USER"
				}
				done := enginetest.LockAsync(e, waiter, ctx, q, false)
				joined := false
				defer func() {
					if !joined {
						cancel()
						<-done
					}
				}()
				enginetest.LockPending(t, e, 1)
				if e.CatalogRead().State() != before.State() {
					t.Fatal("blocked mutation published")
				}
				enginetest.SessionSQL(t, owner, "COMMIT")
				err := <-done
				joined = true
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("old target was retargeted after lock wait", err)
				}
				after := e.CatalogRead()
				if change == "recreate" {
					replacement := after.NameAddress(key)
					if replacement == address || replacement == (ids.ObjectAddress{}) || after.Get(replacement).(*catalog.EventTrigger).FiringMode != catalog.TriggerOrigin {
						t.Fatal("mutated replacement identity")
					}
				} else if after.NameAddress(key) != (ids.ObjectAddress{}) {
					t.Fatal("resurrected old binding")
				}
				if before.NameAddress(key) != address || before.Get(address).Name() != "et_admit" {
					t.Fatal("retained original changed")
				}
			})
		}
	}
}

func eventTriggerDDLPublicationFences(t *testing.T) {
	for _, change := range []string{"role", "session", "outer_role", "replacement", "completed_transaction", "standalone"} {
		t.Run(change, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE FUNCTION et_fence_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$", "CREATE EVENT TRIGGER et_fence ON sql_drop EXECUTE FUNCTION et_fence_fn()"} {
				enginetest.SessionSQL(t, actor, q)
			}
			before := e.CatalogRead()
			key := ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "et_fence"}
			address := before.NameAddress(key)
			if change == "completed_transaction" {
				ref := enginetest.TransactionBegin(t, actor)
				enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_fence ENABLE ALWAYS")
				if e.CatalogRead().State() != before.State() || actor.Transactions.Catalog == nil {
					t.Fatal("transaction mutation did not remain private")
				}
				// The successful statement consumed its execution fence. Later
				// unrelated role work must not veto this transaction's COMMIT.
				enginetest.SessionSQL(t, peer, "CREATE ROLE et_fence_peer")
				enginetest.TransactionEnd(t, actor, ref, true)
				if e.CatalogRead().Get(address).(*catalog.EventTrigger).FiringMode != catalog.TriggerAlways || before.Get(address).(*catalog.EventTrigger).FiringMode != catalog.TriggerOrigin {
					t.Fatal("completed statement authority leaked into COMMIT")
				}
				return
			}
			if change == "replacement" {
				// Real private Execute/Finish first transfers the mutation to its owning
				// transaction; replacement retires that journal's actual epoch.
				ref := enginetest.TransactionBegin(t, actor)
				enginetest.SessionSQL(t, actor, "ALTER EVENT TRIGGER et_fence ENABLE ALWAYS")
				journal := actor.Transactions.Catalog
				if journal == nil || e.CatalogRead().State() != before.State() {
					t.Fatal("missing unpublished transaction")
				}
				if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
				// The real commit boundary owns the epoch check; descriptor-only
				// CatalogPreflightLocked deliberately has no transaction epoch.
				op := enginetest.TransactionOperation(t, actor)
				_, err := e.Commit(op, ref)
				op.Done()
				if !errors.Is(err, ids.ErrCatalogReplaced) {
					t.Fatal("retired transaction admitted", err)
				}
				expectEventIndex(t, e.CatalogRead(), 0)
				expectEventIndex(t, before, 1)
				return
			}
			op, err := actor.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished authority fence"))
			if change == "standalone" {
				if _, err = scope.Execute(enginetest.Statement(t, "ALTER EVENT TRIGGER et_fence ENABLE ALWAYS"), 0, nil); err != nil {
					t.Fatal(err)
				}
				published := e.CatalogRead()
				if !scope.Completed || scope.Catalog != nil || scope.CatalogAuthority != nil || published.Get(address).(*catalog.EventTrigger).FiringMode != catalog.TriggerAlways {
					t.Fatal("standalone utility lost immediate completion")
				}
				if err = scope.Finish(nil); err != nil || e.CatalogRead().State() != published.State() {
					t.Fatal("standalone Finish republished", err)
				}
				return
			}
			previous, err := scope.BeginEventCommand("DROP TABLE")
			if err != nil {
				t.Fatal(err)
			}
			if change == "outer_role" {
				e.Mu.Lock()
				auth, authErr := e.AuthorizationLocked(op)
				if authErr == nil {
					scope.CatalogAuthority = auth.CatalogAuthorityLocked(ids.ObjectRef{}, nil)
				}
				e.Mu.Unlock()
				if authErr != nil {
					t.Fatal(authErr)
				}
			}
			outerAuthority := scope.CatalogAuthority
			if _, err = scope.Execute(enginetest.Statement(t, "ALTER EVENT TRIGGER et_fence ENABLE ALWAYS"), 0, nil); err != nil {
				t.Fatal(err)
			}
			if scope.CatalogAuthority == nil || scope.CatalogAuthority.Session != actor || scope.CatalogAuthority.Authorization != actor.Authorization.Revision || scope.CatalogAuthority.PublishedRoles != e.RoleStore.Published {
				t.Fatal("pending mutation lost original Finish authority")
			}
			if outerAuthority != nil && scope.CatalogAuthority != outerAuthority {
				t.Fatal("lifecycle transfer overwrote outer authority")
			}
			if err = scope.FinishEventCommand(previous, nil); err != nil {
				t.Fatal(err)
			}
			if change == "session" {
				actor.Mu.Lock()
				actor.Authorization.Revision++
				actor.Mu.Unlock()
			} else {
				enginetest.SessionSQL(t, peer, "CREATE ROLE et_fence_peer")
			}
			incumbent := e.CatalogRead()
			var conflict catalog.Conflict
			if err = scope.Finish(nil); !errors.As(err, &conflict) {
				t.Fatal("Finish did not reject changed authority", err)
			}
			if e.CatalogRead().State() != incumbent.State() {
				t.Fatal("stale authority published")
			}
			for _, d := range e.CatalogRead().EventTriggers() {
				if d.FiringMode != catalog.TriggerOrigin {
					t.Fatal("rejected mode installed")
				}
			}
		})
	}
}

func eventTriggerDDLPublicationRoutineDepth(t *testing.T) {
	for _, action := range []string{"alter", "drop"} {
		for _, finish := range []string{"success", "role", "session"} {
			t.Run(action+"/"+finish, func(t *testing.T) {
				e := engine.New()
				actor := enginetest.StartSession(t, e, nil)
				peer := enginetest.StartSession(t, e, nil)
				command := "ALTER EVENT TRIGGER et_depth ENABLE ALWAYS"
				if action == "drop" {
					command = "DROP EVENT TRIGGER et_depth"
				}
				for _, q := range []string{
					"CREATE FUNCTION et_depth_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
					"CREATE EVENT TRIGGER et_depth ON sql_drop EXECUTE FUNCTION et_depth_fn()",
					"CREATE FUNCTION et_depth_mutate() RETURNS void LANGUAGE SQL AS $$" + command + "$$",
				} {
					enginetest.SessionSQL(t, actor, q)
				}
				before := e.CatalogRead()
				key := ids.NameKey{ClassID: ids.EventTriggerClassID, Name: "et_depth"}
				address := before.NameAddress(key)
				original := before.Get(address).(*catalog.EventTrigger)
				originalAuthorization := actor.Authorization.Revision
				originalRoles := e.RoleStore.Published
				op, err := actor.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				defer op.Done()
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				defer scope.Finish(errors.New("unfinished depth-only routine"))
				if scope.UsesCatalogOverlay() || scope.Catalog != nil || scope.Owner.Catalog != nil || scope.CatalogAuthority != nil || scope.EventCommand != nil || core.ScopeActiveSQLRoutineDepth(scope) != 0 {
					t.Fatal("routine witness already has an overlay route")
				}
				// The routine's first and only body statement is ineligible for an
				// EventCommand. Real invocation depth, not a preopened journal or
				// an injected depth field, must defer this lifecycle mutation.
				result, err := scope.Execute(enginetest.Statement(t, "SELECT et_depth_mutate()"), 0, nil)
				if err != nil {
					t.Fatal(err)
				}
				if result.Rows == nil {
					t.Fatal("routine SELECT lost its result")
				}
				if err = result.Rows.Close(); err != nil {
					t.Fatal(err)
				}
				if scope.Completed || scope.Catalog == nil || scope.EventCommand != nil || core.ScopeActiveSQLRoutineDepth(scope) != 0 || e.CatalogRead().State() != before.State() {
					t.Fatal("routine did not leave a private pending mutation with balanced depth")
				}
				authority := scope.CatalogAuthority
				if authority == nil || authority.Session != actor || authority.Authorization != originalAuthorization || authority.PublishedRoles != originalRoles {
					t.Fatal("depth-only mutation lost its original admission authority")
				}
				private := e.PrivateCatalogLocked(scope.PendingCatalog())
				next := private.Get(address)
				if action == "drop" {
					if next != nil || private.NameAddress(key) != (ids.ObjectAddress{}) {
						t.Fatal("routine DROP did not remove its actual target privately")
					}
				} else if next == original || next.(*catalog.EventTrigger).FiringMode != catalog.TriggerAlways || private.NameAddress(key) != address {
					t.Fatal("routine ALTER did not stage its immutable successor")
				}
				if finish == "role" {
					enginetest.SessionSQL(t, peer, "CREATE ROLE et_depth_peer")
				} else if finish == "session" {
					actor.Mu.Lock()
					actor.Authorization.Revision++
					actor.Mu.Unlock()
				}
				incumbent := e.CatalogRead()
				err = scope.Finish(nil)
				if finish == "success" {
					installed := e.CatalogRead()
					if err != nil || installed.Get(address) != next || installed.NameAddress(key) != private.NameAddress(key) {
						t.Fatal("routine Finish did not publish its exact successor", err)
					}
					if action == "drop" {
						expectEventIndex(t, installed, 0)
					} else {
						expectEventIndex(t, installed, 1)
					}
					if err = scope.Finish(nil); err != nil || e.CatalogRead().State() != installed.State() {
						t.Fatal("routine Finish published twice", err)
					}
				} else {
					var conflict catalog.Conflict
					if !errors.As(err, &conflict) || e.CatalogRead().State() != incumbent.State() || incumbent.Get(address) != original || incumbent.NameAddress(key) != address {
						t.Fatal("depth-only stale authority published", err)
					}
					expectEventIndex(t, incumbent, 1)
				}
				if before.Get(address) != original || original.FiringMode != catalog.TriggerOrigin || before.NameAddress(key) != address {
					t.Fatal("routine mutated retained predecessor")
				}
			})
		}
	}
}
